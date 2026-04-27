// Package seed implements the in-process catalog seeding loop introduced
// in PRD-30. It replaces the former seed-catalog.sh bash script that ran
// as a Kubernetes Job with a ConfigMap mount.
package seed

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/accelbench/accelbench/internal/database"
	"github.com/accelbench/accelbench/internal/recommend"
	"github.com/google/uuid"
)

// Options controls one seeding run.
type Options struct {
	DryRun  bool
	HfToken string // empty until PRD-31 plumbs Secrets Manager
}

// Repo is the subset of the database interface the seeder needs.
type Repo interface {
	LoadCatalogMatrix(ctx context.Context) (*database.CatalogMatrix, error)
	GetToolVersions(ctx context.Context) (*database.ToolVersions, error)
	ModelCacheByHfID(ctx context.Context) (map[string]database.ModelCache, error)
	ListRunKeys(ctx context.Context) ([]database.RunKey, error)
	// ListInstanceTypes returns every GPU/Neuron instance type known to the
	// platform. Used by the recommender's larger-instance fallback logic,
	// which the seeder invokes per (model, instance) pair.
	ListInstanceTypes(ctx context.Context) ([]database.InstanceType, error)

	CreateCatalogSeedStatus(ctx context.Context, id string, total int, dryRun bool) error
	UpdateCatalogSeedProgress(ctx context.Context, id string, completed int) error
	CompleteCatalogSeedStatus(ctx context.Context, id string) error
	FailCatalogSeedStatus(ctx context.Context, id, errMsg string) error
	GetActiveCatalogSeed(ctx context.Context) (*database.CatalogSeedStatus, error)
	// PRD-40: claim ownership on the seed row.
	ClaimSeed(ctx context.Context, seedID, pod string) error
}

// ServerDeps is the subset of the api.Server surface the seeder needs.
// Passed as an interface so internal/seed doesn't depend on internal/api.
type ServerDeps interface {
	// FetchModelConfig returns the parsed HF config for a model, preferring a
	// cached config.json from S3 when available. Used by the recommender.
	FetchModelConfig(ctx context.Context, modelID, hfToken string) (*recommend.ModelConfig, error)
	// CreateRun persists a benchmark_run row and kicks off orchestration
	// in a background goroutine. Returns the new run ID.
	CreateRun(ctx context.Context, req *database.RunRequest) (string, error)
}

// Seeder walks the DB-backed catalog matrix and dispatches benchmark runs.
type Seeder struct {
	repo     Repo
	deps     ServerDeps
	hostname string // PRD-40: claimed on seed rows for ownership-aware recovery
}

// New returns a Seeder with the given dependencies. hostname identifies the
// API pod running the seed (PRD-40).
func New(repo Repo, deps ServerDeps, hostname string) *Seeder {
	return &Seeder{repo: repo, deps: deps, hostname: hostname}
}

// Start creates a seed status row and launches the seed loop in a goroutine.
// Returns the seed ID, or an error if a seed is already active.
func (s *Seeder) Start(ctx context.Context, opts Options) (string, error) {
	active, err := s.repo.GetActiveCatalogSeed(ctx)
	if err != nil {
		return "", fmt.Errorf("check active seed: %w", err)
	}
	if active != nil {
		return "", ErrSeedAlreadyRunning
	}

	// Pre-compute total work so the progress bar isn't blank for the first
	// couple seconds. The actual loop re-enumerates the matrix itself.
	matrix, err := s.repo.LoadCatalogMatrix(ctx)
	if err != nil {
		return "", fmt.Errorf("load matrix: %w", err)
	}
	total := enabledPairCount(matrix)

	id := uuid.NewString()
	if err := s.repo.CreateCatalogSeedStatus(ctx, id, total, opts.DryRun); err != nil {
		return "", fmt.Errorf("create seed status: %w", err)
	}
	// PRD-40: claim ownership so orphan recovery on sibling pods leaves it alone.
	if err := s.repo.ClaimSeed(ctx, id, s.hostname); err != nil {
		log.Printf("seed %s: claim ownership: %v", id, err)
	}

	go s.run(id, opts)
	return id, nil
}

// ErrSeedAlreadyRunning is returned by Start when another seed is active.
var ErrSeedAlreadyRunning = errSeedAlreadyRunning{}

type errSeedAlreadyRunning struct{}

func (errSeedAlreadyRunning) Error() string { return "catalog seed already running" }

// run is the goroutine body. Uses a detached context so it survives the
// HTTP request that triggered Start, but respects cancellation at iteration
// boundaries.
func (s *Seeder) run(id string, opts Options) {
	ctx := context.Background()

	matrix, err := s.repo.LoadCatalogMatrix(ctx)
	if err != nil {
		s.finish(ctx, id, fmt.Errorf("load matrix: %w", err))
		return
	}
	existingKeys, err := s.repo.ListRunKeys(ctx)
	if err != nil {
		s.finish(ctx, id, fmt.Errorf("list run keys: %w", err))
		return
	}
	existing := make(map[string]bool, len(existingKeys))
	for _, k := range existingKeys {
		existing[k.ModelHfID+"|"+k.InstanceTypeName] = true
	}
	cache, err := s.repo.ModelCacheByHfID(ctx)
	if err != nil {
		s.finish(ctx, id, fmt.Errorf("load model cache: %w", err))
		return
	}
	tv, err := s.repo.GetToolVersions(ctx)
	if err != nil {
		s.finish(ctx, id, fmt.Errorf("load tool versions: %w", err))
		return
	}

	// Snapshot every known instance type once — the recommender's
	// larger-instance fallback scans this list.
	allInstTypes, err := s.repo.ListInstanceTypes(ctx)
	if err != nil {
		s.finish(ctx, id, fmt.Errorf("list instance types: %w", err))
		return
	}
	allSpecs := make([]recommend.InstanceSpec, 0, len(allInstTypes))
	for _, it := range allInstTypes {
		allSpecs = append(allSpecs, recommend.InstanceSpec{
			Name:                 it.Name,
			AcceleratorType:      it.AcceleratorType,
			AcceleratorName:      it.AcceleratorName,
			AcceleratorCount:     it.AcceleratorCount,
			AcceleratorMemoryGiB: it.AcceleratorMemoryGiB,
			MemoryGiB:            it.MemoryGiB,
		})
	}
	specByName := make(map[string]recommend.InstanceSpec, len(allSpecs))
	for _, sp := range allSpecs {
		specByName[sp.Name] = sp
	}

	completed := 0
	for _, m := range matrix.Models {
		if !m.Enabled {
			continue
		}

		// Prefer S3-cached weights when available. No HF token is needed for
		// cached models even for metadata (fetchModelConfig reads config.json
		// from S3 when the model is cached).
		s3URI := ""
		if c, ok := cache[m.HfID]; ok && c.Status == "cached" {
			s3URI = c.S3URI
		}

		// Fetch model config once per model — the recommender consumes it for
		// every (model, instance) pair below. If the fetch fails (HF 401,
		// gated model, etc.), log and skip the model's entire row.
		modelCfg, err := s.deps.FetchModelConfig(ctx, m.HfID, opts.HfToken)
		if err != nil {
			log.Printf("seed %s: skipping model %s: fetch config: %v", id, m.HfID, err)
			for _, it := range matrix.InstanceTypes {
				if it.Enabled {
					completed++
				}
			}
			_ = s.repo.UpdateCatalogSeedProgress(ctx, id, completed)
			continue
		}

		for _, it := range matrix.InstanceTypes {
			if !it.Enabled {
				continue
			}
			completed++

			if existing[m.HfID+"|"+it.Name] {
				_ = s.repo.UpdateCatalogSeedProgress(ctx, id, completed)
				continue
			}

			inst, ok := specByName[it.Name]
			if !ok {
				log.Printf("seed %s: skipping %s × %s: unknown instance type", id, m.HfID, it.Name)
				_ = s.repo.UpdateCatalogSeedProgress(ctx, id, completed)
				continue
			}

			var rec *recommend.Recommendation
			if strings.EqualFold(inst.AcceleratorType, "neuron") {
				rec = recommend.RecommendNeuron(*modelCfg, inst)
			} else {
				rec = recommend.Recommend(*modelCfg, inst, allSpecs, recommend.RecommendOptions{
					VLLMVersion: tv.FrameworkVersion,
				})
			}
			if rec == nil || !rec.Explanation.Feasible {
				reason := "unknown"
				if rec != nil && rec.Explanation.Reason != "" {
					reason = rec.Explanation.Reason
				}
				log.Printf("seed %s: skipping %s × %s: infeasible (%s)", id, m.HfID, it.Name, reason)
				_ = s.repo.UpdateCatalogSeedProgress(ctx, id, completed)
				continue
			}

			if opts.DryRun {
				log.Printf("seed %s: DRY RUN would submit %s × %s (TP=%d, concurrency=%d)",
					id, m.HfID, it.Name, rec.TensorParallelDegree, rec.Concurrency)
				_ = s.repo.UpdateCatalogSeedProgress(ctx, id, completed)
				continue
			}

			req := &database.RunRequest{
				ModelHfID:            m.HfID,
				ModelHfRevision:      "main",
				InstanceTypeName:     it.Name,
				Framework:            frameworkFor(it.Name),
				FrameworkVersion:     tv.FrameworkVersion,
				TensorParallelDegree: rec.TensorParallelDegree,
				Quantization:         rec.Quantization,
				Concurrency:          rec.Concurrency,
				InputSequenceLength:  rec.InputSequenceLength,
				OutputSequenceLength: rec.OutputSequenceLength,
				MaxModelLen:          rec.MaxModelLen,
				DatasetName:          matrix.Defaults.Dataset,
				RunType:              "catalog",
				ScenarioID:           matrix.Defaults.Scenario,
				ModelS3URI:           s3URI,
				HfToken:              opts.HfToken,
			}

			if _, err := s.deps.CreateRun(ctx, req); err != nil {
				log.Printf("seed %s: create run %s × %s: %v", id, m.HfID, it.Name, err)
				// Non-fatal — keep going. Most common cause is an unrecognized
				// instance type, which we'd rather log and skip than abort the
				// whole seed on.
			}
			_ = s.repo.UpdateCatalogSeedProgress(ctx, id, completed)
		}
	}

	s.finish(ctx, id, nil)
}

func (s *Seeder) finish(ctx context.Context, id string, err error) {
	if err != nil {
		log.Printf("seed %s failed: %v", id, err)
		if ferr := s.repo.FailCatalogSeedStatus(ctx, id, err.Error()); ferr != nil {
			log.Printf("seed %s: failed to record failure: %v", id, ferr)
		}
		return
	}
	if cerr := s.repo.CompleteCatalogSeedStatus(ctx, id); cerr != nil {
		log.Printf("seed %s: failed to mark completed: %v", id, cerr)
	}
}

// frameworkFor picks the right framework string for a given instance type.
// GPU instances use vLLM; Neuron instances use vllm-neuron. The catalog matrix
// currently only lists GPU instances but this keeps the seeder correct if
// Neuron instances are re-enabled later.
func frameworkFor(instanceName string) string {
	switch {
	case len(instanceName) >= 4 && (instanceName[:4] == "inf2" || instanceName[:4] == "trn1" || instanceName[:4] == "trn2"):
		return "vllm-neuron"
	default:
		return "vllm"
	}
}

func enabledPairCount(m *database.CatalogMatrix) int {
	models := 0
	for _, x := range m.Models {
		if x.Enabled {
			models++
		}
	}
	instances := 0
	for _, x := range m.InstanceTypes {
		if x.Enabled {
			instances++
		}
	}
	return models * instances
}
