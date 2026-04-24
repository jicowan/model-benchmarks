package seed

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/accelbench/accelbench/internal/database"
	"github.com/accelbench/accelbench/internal/recommend"
)

// fakeRepo is a minimal Repo implementation for tests. Only covers the
// methods the seeder touches.
type fakeRepo struct {
	mu          sync.Mutex
	matrix      *database.CatalogMatrix
	cache       map[string]database.ModelCache
	runKeys     []database.RunKey
	activeSeed  *database.CatalogSeedStatus
	seedCreated string
	progress    []int
	completed   bool
	failedErr   string
}

func (f *fakeRepo) LoadCatalogMatrix(_ context.Context) (*database.CatalogMatrix, error) {
	return f.matrix, nil
}
func (f *fakeRepo) GetToolVersions(_ context.Context) (*database.ToolVersions, error) {
	return &database.ToolVersions{
		FrameworkVersion:     "v0.19.0",
		InferencePerfVersion: "v0.2.0",
	}, nil
}
func (f *fakeRepo) ModelCacheByHfID(_ context.Context) (map[string]database.ModelCache, error) {
	return f.cache, nil
}
func (f *fakeRepo) ListRunKeys(_ context.Context) ([]database.RunKey, error) {
	return f.runKeys, nil
}
func (f *fakeRepo) GetInstanceTypeByName(_ context.Context, name string) (*database.InstanceType, error) {
	return &database.InstanceType{Name: name, ID: "it-" + name}, nil
}
func (f *fakeRepo) CreateCatalogSeedStatus(_ context.Context, id string, total int, dryRun bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.seedCreated = id
	f.activeSeed = &database.CatalogSeedStatus{ID: id, Status: "active", Total: total, DryRun: dryRun}
	return nil
}
func (f *fakeRepo) ClaimSeed(_ context.Context, _, _ string) error { return nil }
func (f *fakeRepo) UpdateCatalogSeedProgress(_ context.Context, _ string, completed int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.progress = append(f.progress, completed)
	return nil
}
func (f *fakeRepo) CompleteCatalogSeedStatus(_ context.Context, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.completed = true
	f.activeSeed = nil
	return nil
}
func (f *fakeRepo) FailCatalogSeedStatus(_ context.Context, _, errMsg string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failedErr = errMsg
	f.activeSeed = nil
	return nil
}
func (f *fakeRepo) GetActiveCatalogSeed(_ context.Context) (*database.CatalogSeedStatus, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.activeSeed, nil
}

// fakeDeps captures calls from the seeder.
type fakeDeps struct {
	mu        sync.Mutex
	fetchErr  error
	fetchedBy []string
	created   []*database.RunRequest
	createErr error
}

func (f *fakeDeps) FetchModelConfig(_ context.Context, modelID, _ string) (*recommend.ModelConfig, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fetchedBy = append(f.fetchedBy, modelID)
	if f.fetchErr != nil {
		return nil, f.fetchErr
	}
	return &recommend.ModelConfig{}, nil
}
func (f *fakeDeps) CreateRun(_ context.Context, req *database.RunRequest) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.createErr != nil {
		return "", f.createErr
	}
	cp := *req
	f.created = append(f.created, &cp)
	return "run-fake", nil
}

func simpleMatrix() *database.CatalogMatrix {
	return &database.CatalogMatrix{
		Defaults: database.CatalogSeedDefaults{
			Scenario: "chatbot",
			Dataset:  "synthetic",
		},
		Models: []database.CatalogModel{
			{HfID: "meta-llama/Llama-3.1-8B-Instruct", Enabled: true},
			{HfID: "microsoft/Phi-4", Enabled: true},
		},
		InstanceTypes: []database.CatalogInstanceType{
			{Name: "g6e.xlarge", Enabled: true},
			{Name: "p5.48xlarge", Enabled: true},
		},
	}
}

// waitForSeed polls repo until the seed is no longer active.
func waitForSeed(t *testing.T, r *fakeRepo) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		r.mu.Lock()
		done := r.activeSeed == nil
		r.mu.Unlock()
		if done {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("seed goroutine did not finish within 2s")
}

func TestSeeder_DryRunCreatesNoRuns(t *testing.T) {
	repo := &fakeRepo{matrix: simpleMatrix()}
	deps := &fakeDeps{}
	s := New(repo, deps, "test-pod")

	id, err := s.Start(context.Background(), Options{DryRun: true})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if id == "" {
		t.Fatal("expected non-empty seed id")
	}
	waitForSeed(t, repo)

	if !repo.completed {
		t.Error("seed should have completed cleanly in dry-run")
	}
	if len(deps.created) != 0 {
		t.Errorf("dry-run must not create runs, got %d", len(deps.created))
	}
	if len(repo.progress) == 0 {
		t.Error("expected progress updates in dry run")
	}
}

func TestSeeder_CachedModelPopulatesS3URI(t *testing.T) {
	cached := "s3://accelbench-models-820537372947/meta-llama/Llama-3.1-8B-Instruct"
	hf := "meta-llama/Llama-3.1-8B-Instruct"
	repo := &fakeRepo{
		matrix: simpleMatrix(),
		cache: map[string]database.ModelCache{
			hf: {HfID: &hf, S3URI: cached, Status: "cached"},
		},
	}
	deps := &fakeDeps{}
	s := New(repo, deps, "test-pod")

	_, err := s.Start(context.Background(), Options{})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitForSeed(t, repo)

	if !repo.completed {
		t.Fatalf("seed did not complete: %v", repo.failedErr)
	}
	// 2 models × 2 instances = 4 runs
	if len(deps.created) != 4 {
		t.Fatalf("expected 4 runs, got %d", len(deps.created))
	}
	// Cached model → S3 URI populated; uncached model → empty.
	for _, req := range deps.created {
		switch req.ModelHfID {
		case hf:
			if req.ModelS3URI != cached {
				t.Errorf("cached model run missing S3 URI: got %q want %q", req.ModelS3URI, cached)
			}
		case "microsoft/Phi-4":
			if req.ModelS3URI != "" {
				t.Errorf("uncached model run has S3 URI: %q", req.ModelS3URI)
			}
		default:
			t.Errorf("unexpected model in run: %s", req.ModelHfID)
		}
	}
}

func TestSeeder_DedupsAgainstExistingRuns(t *testing.T) {
	repo := &fakeRepo{
		matrix: simpleMatrix(),
		runKeys: []database.RunKey{
			// Model 1 × instance 1 already exists — skip it.
			{ModelHfID: "meta-llama/Llama-3.1-8B-Instruct", InstanceTypeName: "g6e.xlarge"},
		},
	}
	deps := &fakeDeps{}
	s := New(repo, deps, "test-pod")

	_, err := s.Start(context.Background(), Options{})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitForSeed(t, repo)

	// 4 total pairs - 1 dedup = 3 runs.
	if len(deps.created) != 3 {
		t.Fatalf("expected 3 runs after dedup, got %d", len(deps.created))
	}
}

func TestSeeder_RejectsConcurrentStart(t *testing.T) {
	repo := &fakeRepo{
		matrix: simpleMatrix(),
		activeSeed: &database.CatalogSeedStatus{
			ID:     "existing-id",
			Status: "active",
		},
	}
	s := New(repo, &fakeDeps{}, "test-pod")

	_, err := s.Start(context.Background(), Options{})
	if !errors.Is(err, ErrSeedAlreadyRunning) {
		t.Errorf("expected ErrSeedAlreadyRunning, got %v", err)
	}
}

func TestSeeder_FetchConfigErrorSkipsModelRow(t *testing.T) {
	repo := &fakeRepo{matrix: simpleMatrix()}
	deps := &fakeDeps{fetchErr: errors.New("HF 401")}
	s := New(repo, deps, "test-pod")

	_, err := s.Start(context.Background(), Options{})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitForSeed(t, repo)

	// With fetch errors on every model, no runs should be created,
	// but the seed should still complete (not fail).
	if !repo.completed {
		t.Errorf("seed should complete even when all models fail metadata fetch")
	}
	if len(deps.created) != 0 {
		t.Errorf("expected 0 runs after fetch errors, got %d", len(deps.created))
	}
}

func TestSeeder_DisabledRowsSkipped(t *testing.T) {
	m := simpleMatrix()
	m.Models[1].Enabled = false       // Phi-4 disabled
	m.InstanceTypes[1].Enabled = false // p5 disabled
	repo := &fakeRepo{matrix: m}
	deps := &fakeDeps{}
	s := New(repo, deps, "test-pod")

	_, err := s.Start(context.Background(), Options{})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitForSeed(t, repo)

	// Only Llama × g6e.xlarge.
	if len(deps.created) != 1 {
		t.Fatalf("expected 1 run after disable filters, got %d", len(deps.created))
	}
	req := deps.created[0]
	if req.ModelHfID != "meta-llama/Llama-3.1-8B-Instruct" || req.InstanceTypeName != "g6e.xlarge" {
		t.Errorf("unexpected surviving run: %+v", req)
	}
}
