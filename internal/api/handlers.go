package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/accelbench/accelbench/internal/auth"
	"github.com/accelbench/accelbench/internal/cache"
	"github.com/accelbench/accelbench/internal/database"
	"github.com/accelbench/accelbench/internal/orchestrator"
	"github.com/accelbench/accelbench/internal/recommend"
	"github.com/accelbench/accelbench/internal/scenario"
	"github.com/accelbench/accelbench/internal/seed"
	"github.com/accelbench/accelbench/internal/testsuite"

	"golang.org/x/sync/errgroup"
	"k8s.io/client-go/kubernetes"
)

// Server holds dependencies for API handlers.
type Server struct {
	repo       database.Repo
	orch       *orchestrator.Orchestrator
	client     kubernetes.Interface
	hfClient   recommend.HFClientInterface
	seeder     *seed.Seeder
	secrets    SecretsStore
	ec2Client  EC2Client      // PRD-33
	dynClient  DynamicClient  // PRD-33 — client-go/dynamic, typed via an interface for tests
	hostname   string         // PRD-40 — this pod's identifier for replica coordination
	cache      cache.Cache   // PRD-38 — 60s TTL response cache for slow-changing endpoints
	cognitoIDP CognitoIDP    // PRD-43 — Cognito InitiateAuth / GlobalSignOut
	authConfig auth.Config   // PRD-43 — user pool + client ID + AUTH_DISABLED flag
	authVerifier *auth.Verifier // PRD-43 — JWT verifier (for middleware + /auth/me fallback)
}

// NewServer creates a new API server. hostname is the running pod's name
// (os.Hostname in production, any stable string in tests); used for PRD-40
// replica coordination.
func NewServer(repo database.Repo, client kubernetes.Interface, hostname string) *Server {
	s := &Server{
		repo:     repo,
		orch:     orchestrator.New(client, repo, hostname),
		client:   client,
		hfClient: recommend.NewHFClient(),
		hostname: hostname,
		cache:    cache.NopCache{},
		// PRD-43: default to Disabled=true so tests + local dev work without
		// Cognito config. cmd/server/main.go calls SetAuth to flip this off
		// in production, with AUTH_DISABLED as an explicit escape hatch.
		authConfig: auth.Config{Disabled: true},
	}
	s.seeder = seed.New(repo, s, hostname)
	return s
}

// NewServerWithHFClient creates a new API server with a custom HFClient (for testing).
func NewServerWithHFClient(repo database.Repo, client kubernetes.Interface, hfClient recommend.HFClientInterface, hostname string) *Server {
	s := &Server{
		repo:       repo,
		orch:       orchestrator.New(client, repo, hostname),
		client:     client,
		hfClient:   hfClient,
		hostname:   hostname,
		cache:      cache.NopCache{},
		authConfig: auth.Config{Disabled: true},
	}
	s.seeder = seed.New(repo, s, hostname)
	return s
}

// Orchestrator returns the underlying orchestrator so startup code can wire
// the PRD-40 heartbeat + orphan-recovery loops.
func (s *Server) Orchestrator() *orchestrator.Orchestrator { return s.orch }

// SetSecretsStore injects the AWS Secrets Manager wrapper. Called from main
// after construction so tests can leave it nil and the handlers will 500.
func (s *Server) SetSecretsStore(store SecretsStore) {
	s.secrets = store
	s.orch.SetSecretsStore(store)
}

// SetReservationsClients injects the EC2 SDK client + K8s dynamic client
// used by the Capacity Reservations card (PRD-33). Called from main after
// construction; tests can leave these nil and the endpoints will 500.
func (s *Server) SetReservationsClients(ec EC2Client, dyn DynamicClient) {
	s.ec2Client = ec
	s.dynClient = dyn
}

// SetCache injects the PRD-38 response cache. Called from main after
// construction; tests can leave the default NopCache.
func (s *Server) SetCache(c cache.Cache) {
	s.cache = c
}

// SetAuth injects the PRD-43 auth dependencies. Called from main after
// construction; tests that don't exercise auth can leave these nil and
// set AUTH_DISABLED via Config.
func (s *Server) SetAuth(cfg auth.Config, idp CognitoIDP, verifier *auth.Verifier) {
	s.authConfig = cfg
	s.cognitoIDP = idp
	s.authVerifier = verifier
}

// writeCachedJSON marshals v to JSON, stores the bytes in the cache under
// cacheKey, and writes the response. The trailing newline matches the
// behavior of json.NewEncoder(w).Encode used by writeJSON.
func (s *Server) writeCachedJSON(w http.ResponseWriter, cacheKey string, code int, v any) {
	data, err := json.Marshal(v)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "marshal failed")
		return
	}
	data = append(data, '\n')
	s.cache.Set(cacheKey, data)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Cache", "MISS")
	w.WriteHeader(code)
	w.Write(data)
}

// serveCacheHit writes pre-serialized bytes from the cache to the response.
func serveCacheHit(w http.ResponseWriter, data []byte) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Cache", "HIT")
	w.WriteHeader(http.StatusOK)
	w.Write(data)
}

// RecoverOrphanedRuns is retained for test compatibility but no longer
// called from startup — the PRD-40 heartbeat-driven loop
// (Orchestrator.StartOrphanRecoveryLoop) replaces both this and the
// InterruptActiveCatalogSeeds path. If called directly, it runs one pass
// of the new ownership-aware recovery.
func (s *Server) RecoverOrphanedRuns(ctx context.Context) {
	// The underlying orchestrator method still scans by status (not owner)
	// and is scheduled to be removed once all callers migrate. Until then,
	// it's safe to call: it uses the same markFailed + cleanup path as the
	// new loop.
	s.orch.RecoverOrphanedRuns(ctx)
}

// FetchModelConfig returns a ModelConfig for modelID. Resolution order:
//  1. If the model is already cached in S3 (status=cached with a matching
//     hf_id), read config.json from S3. No HF token needed for gated models.
//  2. Otherwise call HuggingFace. If the caller passed a token, use it.
//     Otherwise (PRD-31) fall back to the platform HF token from Secrets
//     Manager. Errors fetching the platform token are swallowed — gated
//     models will fail with HF 401, which is clearer than a Secrets error.
//
// Exported for use by internal/seed.
func (s *Server) FetchModelConfig(ctx context.Context, modelID, hfToken string) (*recommend.ModelConfig, error) {
	if mc, _ := s.repo.GetModelCacheByHfID(ctx, modelID, "main"); mc != nil && mc.Status == "cached" {
		if cfg, err := recommend.FetchModelConfigFromS3(ctx, mc.S3URI); err == nil {
			return cfg, nil
		}
		// Fall through to HF on S3 read failure.
	}
	if hfToken == "" && s.secrets != nil {
		if tok, err := s.secrets.GetHFToken(ctx); err == nil {
			hfToken = tok
		} else {
			log.Printf("resolve platform HF token for fetchModelConfig: %v", err)
		}
	}
	return s.hfClient.FetchModelConfig(modelID, hfToken)
}

// RegisterRoutes registers all API routes on the given mux.
//
// Routing is split into two tiers for PRD-43 auth:
//
//   - Public routes (no auth middleware) go directly on `mux`:
//     POST /api/v1/auth/login, POST /api/v1/auth/refresh. /healthz is
//     also public but is registered by cmd/server/main.go, not here.
//
//   - Protected routes go on a private ServeMux that's wrapped in the
//     auth middleware and then mounted at /api/v1/ on the outer mux.
//     Go 1.22's ServeMux prefers more-specific patterns, so the two
//     public POSTs above take precedence over the /api/v1/ fallback.
func (s *Server) RegisterRoutes(mux *http.ServeMux) {
	// --- Public: auth endpoints that must work before the user has a token ---
	mux.HandleFunc("POST /api/v1/auth/login", s.handleAuthLogin)
	mux.HandleFunc("POST /api/v1/auth/respond-challenge", s.handleAuthRespondChallenge)
	mux.HandleFunc("POST /api/v1/auth/refresh", s.handleAuthRefresh)

	// --- Protected: wrapped in auth.Middleware ---
	// PRD-44: admin is an inner middleware for the Configuration-page
	// surface and platform-mutating ops. It runs after auth.Middleware
	// has put a Principal on the context.
	admin := auth.RequireRole("admin")
	// PRD-48: routes wrapped in `nonViewer` are accessible to admin
	// and user but not viewer (submit-run, cancel, seed status, etc.).
	// Routes not wrapped in any role gate default to accessible by all
	// authenticated roles including viewer — the read-only surface.
	nonViewer := auth.AllowRoles("admin", "user")
	p := http.NewServeMux()
	p.HandleFunc("POST /api/v1/auth/logout", s.handleAuthLogout)
	p.HandleFunc("GET /api/v1/auth/me", s.handleAuthMe)

	// Reader allow-list — accessible by admin, user, AND viewer. These
	// power the Dashboard, Catalog, Compare pages and run/suite detail
	// pages plus their CSV / K8s-manifest exports.
	p.HandleFunc("GET /api/v1/status", s.handleStatus)
	p.HandleFunc("GET /api/v1/catalog", s.handleListCatalog)
	p.HandleFunc("GET /api/v1/runs/{id}", s.handleGetRun)
	p.HandleFunc("GET /api/v1/runs/{id}/metrics", s.handleGetMetrics)
	p.HandleFunc("GET /api/v1/pricing", s.handleListPricing)
	// PRD-48 follow-up: the Dashboard's 14-day activity chart + Recent
	// Runs table pull from /jobs and /suite-runs. Viewers need the
	// lists (not just the stats aggregate) so those panels render
	// under the view-only role. Clicking a row navigates to
	// /results/{id} or /suite-runs/{id}, both already on the allow-list.
	p.HandleFunc("GET /api/v1/jobs", s.handleListRuns)
	p.HandleFunc("GET /api/v1/suite-runs", s.handleListSuiteRuns)
	p.HandleFunc("GET /api/v1/suite-runs/{id}", s.handleGetSuiteRun)
	// PRD-41 / PRD-48: report exports. Print is client-side (browser
	// dialog) and needs no endpoint; these four cover CSV + K8s YAML.
	p.HandleFunc("GET /api/v1/runs/{id}/export", s.handleExportManifest)
	p.HandleFunc("GET /api/v1/runs/{id}/csv", s.handleExportRunCSV)
	p.HandleFunc("GET /api/v1/suite-runs/{id}/csv", s.handleExportSuiteCSV)
	p.HandleFunc("GET /api/v1/suite-runs/{id}/export", s.handleExportSuiteManifest)
	p.HandleFunc("GET /api/v1/compare/csv", s.handleExportCompareCSV)

	// Admin + user only — drives the New Benchmark, Runs, Estimate,
	// Model Cache pages (pages viewers never navigate to). Submission
	// + cancel + delete mutations also live here because viewer is
	// read-only by definition.
	p.Handle("POST /api/v1/runs", nonViewer(http.HandlerFunc(s.handleCreateRun)))
	p.Handle("POST /api/v1/runs/{id}/cancel", nonViewer(http.HandlerFunc(s.handleCancelRun)))
	p.Handle("DELETE /api/v1/runs/{id}", nonViewer(http.HandlerFunc(s.handleDeleteRun)))
	p.Handle("GET /api/v1/instance-types", nonViewer(http.HandlerFunc(s.handleListInstanceTypes)))
	p.Handle("GET /api/v1/recommend", nonViewer(http.HandlerFunc(s.handleRecommend)))
	p.Handle("GET /api/v1/estimate", nonViewer(http.HandlerFunc(s.handleEstimate)))
	p.Handle("POST /api/v1/catalog/seed", admin(http.HandlerFunc(s.handleCatalogSeed)))
	p.Handle("GET /api/v1/catalog/seed", nonViewer(http.HandlerFunc(s.handleCatalogSeedStatus)))
	// PRD-15: Memory breakdown and OOM history drive the New Benchmark
	// form, so non-viewer only.
	p.Handle("GET /api/v1/memory-breakdown", nonViewer(http.HandlerFunc(s.handleMemoryBreakdown)))
	p.Handle("GET /api/v1/oom-history", nonViewer(http.HandlerFunc(s.handleOOMHistory)))
	// PRD-12/13: Scenarios and test suites
	p.Handle("GET /api/v1/scenarios", nonViewer(http.HandlerFunc(s.handleListScenarios)))
	p.Handle("GET /api/v1/test-suites", nonViewer(http.HandlerFunc(s.handleListTestSuites)))
	p.Handle("POST /api/v1/suite-runs", nonViewer(http.HandlerFunc(s.handleCreateSuiteRun)))
	// PRD-20: Model cache management. Mutations are admin-only; list
	// + per-item GET are admin + user (drives the New Benchmark form's
	// S3-cache toggle and the Model Cache page) — not viewer.
	p.Handle("GET /api/v1/model-cache", nonViewer(http.HandlerFunc(s.handleListModelCache)))
	p.Handle("GET /api/v1/model-cache/stats", nonViewer(http.HandlerFunc(s.handleModelCacheStats))) // PRD-35
	p.Handle("GET /api/v1/model-cache/{id}", nonViewer(http.HandlerFunc(s.handleGetModelCache)))
	p.Handle("POST /api/v1/model-cache", admin(http.HandlerFunc(s.handleCreateModelCache)))
	p.Handle("DELETE /api/v1/model-cache/{id}", admin(http.HandlerFunc(s.handleDeleteModelCache)))
	p.Handle("POST /api/v1/model-cache/register", admin(http.HandlerFunc(s.handleRegisterCustomModel)))

	// PRD-44: the entire /api/v1/config/* surface and all Configuration-
	// page operations are admin-only. Non-admin authenticated users
	// cannot reach any of these endpoints.

	// PRD-31: Credentials management (HF token + Docker Hub token)
	p.Handle("GET /api/v1/config/credentials", admin(http.HandlerFunc(s.handleGetCredentials)))
	p.Handle("PUT /api/v1/config/credentials/hf-token", admin(http.HandlerFunc(s.handlePutHFToken)))
	p.Handle("DELETE /api/v1/config/credentials/hf-token", admin(http.HandlerFunc(s.handleDeleteHFToken)))
	p.Handle("PUT /api/v1/config/credentials/dockerhub-token", admin(http.HandlerFunc(s.handlePutDockerHubToken)))
	p.Handle("DELETE /api/v1/config/credentials/dockerhub-token", admin(http.HandlerFunc(s.handleDeleteDockerHubToken)))
	// PRD-32: Catalog matrix editor, scenario overrides, registry, audit log
	p.Handle("GET /api/v1/config/catalog-matrix", admin(http.HandlerFunc(s.handleGetCatalogMatrix)))
	p.Handle("PUT /api/v1/config/catalog-matrix", admin(http.HandlerFunc(s.handlePutCatalogMatrix)))
	p.Handle("GET /api/v1/config/scenario-overrides", admin(http.HandlerFunc(s.handleListScenarioOverrides)))
	p.Handle("PUT /api/v1/config/scenario-overrides/{id}", admin(http.HandlerFunc(s.handlePutScenarioOverride)))
	p.Handle("DELETE /api/v1/config/scenario-overrides/{id}", admin(http.HandlerFunc(s.handleDeleteScenarioOverride)))
	p.Handle("GET /api/v1/config/registry", admin(http.HandlerFunc(s.handleGetRegistry)))
	p.Handle("GET /api/v1/config/audit-log", admin(http.HandlerFunc(s.handleListAuditLog)))
	// PRD-33: Capacity Reservations card
	p.Handle("GET /api/v1/config/capacity-reservations", admin(http.HandlerFunc(s.handleListReservations)))
	p.Handle("POST /api/v1/config/capacity-reservations", admin(http.HandlerFunc(s.handlePostReservation)))
	p.Handle("DELETE /api/v1/config/capacity-reservations/{node_class}/{reservation_id}", admin(http.HandlerFunc(s.handleDeleteReservation)))
	// PRD-34: Tool Versions (vLLM framework + inference-perf)
	p.Handle("GET /api/v1/config/tool-versions", admin(http.HandlerFunc(s.handleGetToolVersions)))
	p.Handle("PUT /api/v1/config/tool-versions", admin(http.HandlerFunc(s.handlePutToolVersions)))
	// PRD-45: user management (admin-only)
	p.Handle("GET /api/v1/users", admin(http.HandlerFunc(s.handleListUsers)))
	p.Handle("POST /api/v1/users", admin(http.HandlerFunc(s.handleCreateUser)))
	p.Handle("PATCH /api/v1/users/{sub}", admin(http.HandlerFunc(s.handleUpdateUserRole)))
	p.Handle("POST /api/v1/users/{sub}/disable", admin(http.HandlerFunc(s.handleDisableUser)))
	p.Handle("POST /api/v1/users/{sub}/enable", admin(http.HandlerFunc(s.handleEnableUser)))
	p.Handle("POST /api/v1/users/{sub}/reset-password", admin(http.HandlerFunc(s.handleResetUserPassword)))
	p.Handle("POST /api/v1/users/{sub}/resend-invite", admin(http.HandlerFunc(s.handleResendInvite)))
	p.Handle("DELETE /api/v1/users/{sub}", admin(http.HandlerFunc(s.handleDeleteUser)))

	// PRD-35: Dashboard aggregate stats (every card on the Dashboard).
	p.HandleFunc("GET /api/v1/dashboard/stats", s.handleDashboardStats)

	// Mount the protected subrouter behind auth middleware. The two
	// public /api/v1/auth/* POSTs registered directly on `mux` above
	// take precedence by Go 1.22's longest-pattern rule.
	mux.Handle("/api/v1/", auth.Middleware(s.authConfig, s.authVerifier)(p))
}

func (s *Server) handleListCatalog(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := database.CatalogFilter{
		ModelHfID:       q.Get("model"),
		ModelType:       q.Get("model_type"),
		InstanceFamily:  q.Get("instance_family"),
		AcceleratorType: q.Get("accelerator_type"),
		SortBy:          q.Get("sort"),
		SortDesc:        q.Get("order") == "desc",
	}
	if ids := q.Get("ids"); ids != "" {
		// Compare passes a comma-separated list of run IDs so it can fetch
		// exactly the rows it needs without a full catalog scan.
		for _, id := range strings.Split(ids, ",") {
			if id = strings.TrimSpace(id); id != "" {
				f.RunIDs = append(f.RunIDs, id)
			}
		}
	}
	if v := q.Get("limit"); v != "" {
		fmt.Sscanf(v, "%d", &f.Limit)
	}
	if v := q.Get("offset"); v != "" {
		fmt.Sscanf(v, "%d", &f.Offset)
	}

	entries, total, err := s.repo.ListCatalog(r.Context(), f)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "catalog query failed")
		return
	}
	if entries == nil {
		entries = []database.CatalogEntry{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"rows":  entries,
		"total": total,
	})
}

func (s *Server) handleCreateRun(w http.ResponseWriter, r *http.Request) {
	var req database.RunRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	runID, err := s.CreateRun(r.Context(), &req)
	if err != nil {
		var crErr *createRunError
		if errors.As(err, &crErr) {
			writeError(w, crErr.status, crErr.msg)
			return
		}
		writeError(w, http.StatusInternalServerError, "create run failed")
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{
		"id":     runID,
		"status": "pending",
	})
}

// createRunError carries an HTTP status + message for callers that want to
// distinguish user errors from internal ones (the seeder doesn't, but the
// HTTP handler does).
type createRunError struct {
	status int
	msg    string
}

func (e *createRunError) Error() string { return e.msg }

// CreateRun is the internal entry point shared by handleCreateRun and the
// catalog seeder. Returns the new run ID or a *createRunError on user error,
// or another error on internal failure. The orchestrator is kicked off in
// a background goroutine on success.
func (s *Server) CreateRun(ctx context.Context, req *database.RunRequest) (string, error) {
	// For S3-only models, derive model_hf_id from the S3 URI if not provided
	if req.ModelHfID == "" && req.ModelS3URI != "" {
		req.ModelHfID = req.ModelS3URI
	}

	if req.ModelHfID == "" {
		return "", &createRunError{http.StatusBadRequest, "model_hf_id or model_s3_uri is required"}
	}

	// Look up or auto-register model.
	model, err := s.repo.EnsureModel(ctx, req.ModelHfID, req.ModelHfRevision)
	if err != nil {
		return "", fmt.Errorf("ensure model: %w", err)
	}

	// Look up instance type.
	instType, err := s.repo.GetInstanceTypeByName(ctx, req.InstanceTypeName)
	if err != nil {
		return "", fmt.Errorf("lookup instance type: %w", err)
	}
	if instType == nil {
		return "", &createRunError{http.StatusNotFound, fmt.Sprintf("instance type %s not found", req.InstanceTypeName)}
	}

	// Default dataset from scenario if not provided
	datasetName := req.DatasetName
	scenarioID := req.ScenarioID
	if scenarioID == "" {
		// For backwards compatibility, check if RunType contains a scenario ID
		if scn := scenario.Get(req.RunType); scn != nil {
			scenarioID = req.RunType
			req.ScenarioID = scenarioID // Ensure orchestrator sees the scenario
			if datasetName == "" {
				datasetName = scn.Dataset
			}
		}
	} else if datasetName == "" {
		if scn := scenario.Get(scenarioID); scn != nil {
			datasetName = scn.Dataset
		}
	}
	if datasetName == "" {
		datasetName = "synthetic" // fallback default
	}

	// PRD-42: require a scenario. Every live caller (UI, seeder, CLI after
	// this PRD) passes one; direct API callers that don't get a loud 400.
	if scenarioID == "" {
		return "", &createRunError{
			http.StatusBadRequest,
			"scenario_id is required",
		}
	}

	// Determine run_type: 'catalog' for seeded runs, 'on_demand' for user-initiated
	runType := req.RunType
	if runType != "catalog" {
		runType = "on_demand"
	}

	// Create the benchmark run record.
	var scenarioPtr *string
	if scenarioID != "" {
		scenarioPtr = &scenarioID
	}
	var s3URIPtr *string
	if req.ModelS3URI != "" && strings.HasPrefix(req.ModelS3URI, "s3://") {
		u := req.ModelS3URI
		s3URIPtr = &u
	}
	var mnbtPtr *int
	if req.MaxNumBatchedTokens > 0 {
		n := req.MaxNumBatchedTokens
		mnbtPtr = &n
	}
	var kvDtypePtr *string
	if req.KVCacheDtype != "" {
		v := req.KVCacheDtype
		kvDtypePtr = &v
	}
	run := &database.BenchmarkRun{
		ModelID:              model.ID,
		InstanceTypeID:       instType.ID,
		Framework:            req.Framework,
		FrameworkVersion:     req.FrameworkVersion,
		TensorParallelDegree: req.TensorParallelDegree,
		Quantization:         req.Quantization,
		Concurrency:          req.Concurrency,
		InputSequenceLength:  req.InputSequenceLength,
		OutputSequenceLength: req.OutputSequenceLength,
		DatasetName:          datasetName,
		RunType:              runType,
		ScenarioID:           scenarioPtr,
		MaxModelLen:          req.MaxModelLen,
		MaxNumBatchedTokens:  mnbtPtr,
		KVCacheDtype:         kvDtypePtr,
		ModelS3URI:           s3URIPtr,
		Status:               "pending",
	}

	runID, err := s.repo.CreateBenchmarkRun(ctx, run)
	if err != nil {
		return "", fmt.Errorf("create benchmark run: %w", err)
	}

	// Launch orchestration in the background with a detached context
	// so it isn't canceled when the HTTP response is sent.
	go func() {
		cfg := orchestrator.RunConfig{
			RunID:        runID,
			Model:        model,
			InstanceType: instType,
			Request:      req,
		}
		if err := s.orch.Execute(context.Background(), cfg); err != nil {
			log.Printf("benchmark run %s failed: %v", runID, err)
		}
	}()

	return runID, nil
}

// runDetailResponse is the response for GET /api/v1/runs/{id}. The base
// fields (BenchmarkRun + model/instance enrichment) are always present.
// Optional sub-objects appear only when the caller passes ?include=token.
type runDetailResponse struct {
	*database.BenchmarkRun
	ModelHfID        string                     `json:"model_hf_id,omitempty"`
	InstanceTypeName string                     `json:"instance_type_name,omitempty"`
	Metrics          *database.BenchmarkMetrics `json:"metrics,omitempty"`
	Instance         *database.InstanceType     `json:"instance,omitempty"`
	Pricing          *database.PricingRow       `json:"pricing,omitempty"`
	OOM              *database.OOMHistory       `json:"oom,omitempty"`
	Errors           map[string]string          `json:"errors,omitempty"`
}

func (s *Server) handleGetRun(w http.ResponseWriter, r *http.Request) {
	runID := r.PathValue("id")
	run, err := s.repo.GetBenchmarkRun(r.Context(), runID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	if run == nil {
		writeError(w, http.StatusNotFound, "run not found")
		return
	}

	resp := runDetailResponse{BenchmarkRun: run}
	if details, _ := s.repo.GetRunExportDetails(r.Context(), runID); details != nil {
		resp.ModelHfID = details.ModelHfID
		resp.InstanceTypeName = details.InstanceTypeName
	}

	includes := parseIncludes(r.URL.Query().Get("include"))
	if includes != nil {
		s.fetchRunIncludes(r.Context(), &resp, includes)
	}

	// ETag only for bare requests on terminal runs (PRD-38). With
	// ?include= the response varies too much for a stable ETag.
	if includes == nil && (run.Status == "completed" || run.Status == "failed") {
		data, err := json.Marshal(resp)
		if err == nil {
			data = append(data, '\n')
			etag := etagOf(data)
			if checkETag(w, r, etag) {
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write(data)
			return
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

// fetchRunIncludes populates the optional sub-objects on resp in parallel.
// Each goroutine catches its own error and records it in resp.Errors so
// other includes still succeed (partial-failure semantics).
func (s *Server) fetchRunIncludes(ctx context.Context, resp *runDetailResponse, includes IncludeSet) {
	var (
		mu sync.Mutex
		g  errgroup.Group
	)
	setErr := func(token, msg string) {
		mu.Lock()
		if resp.Errors == nil {
			resp.Errors = make(map[string]string)
		}
		resp.Errors[token] = msg
		mu.Unlock()
	}

	if includes.Has("metrics") {
		g.Go(func() error {
			m, err := s.repo.GetMetricsByRunID(ctx, resp.ID)
			if err != nil {
				setErr("metrics", err.Error())
				return nil
			}
			mu.Lock()
			resp.Metrics = m
			mu.Unlock()
			return nil
		})
	}

	if includes.Has("instance") {
		g.Go(func() error {
			it, err := s.repo.GetInstanceTypeByID(ctx, resp.InstanceTypeID)
			if err != nil {
				setErr("instance", err.Error())
				return nil
			}
			mu.Lock()
			resp.Instance = it
			mu.Unlock()
			return nil
		})
	}

	if includes.Has("pricing") {
		g.Go(func() error {
			region := os.Getenv("AWS_REGION")
			if region == "" {
				region = "us-east-2"
			}
			p, err := s.repo.GetPricingForInstanceType(ctx, resp.InstanceTypeID, region)
			if err != nil {
				setErr("pricing", err.Error())
				return nil
			}
			if p != nil {
				mu.Lock()
				resp.Pricing = &database.PricingRow{
					InstanceTypeName:     resp.InstanceTypeName,
					OnDemandHourlyUSD:    p.OnDemandHourlyUSD,
					Reserved1YrHourlyUSD: p.Reserved1YrHourlyUSD,
					Reserved3YrHourlyUSD: p.Reserved3YrHourlyUSD,
					EffectiveDate:        p.EffectiveDate,
				}
				mu.Unlock()
			}
			return nil
		})
	}

	if includes.Has("oom") && resp.ModelHfID != "" && resp.InstanceTypeName != "" {
		g.Go(func() error {
			h, err := s.repo.GetOOMHistory(ctx, resp.ModelHfID, resp.InstanceTypeName, 10)
			if err != nil {
				setErr("oom", err.Error())
				return nil
			}
			mu.Lock()
			resp.OOM = h
			mu.Unlock()
			return nil
		})
	}

	g.Wait()

	if len(resp.Errors) == 0 {
		resp.Errors = nil
	}
}

func (s *Server) handleGetMetrics(w http.ResponseWriter, r *http.Request) {
	runID := r.PathValue("id")
	m, err := s.repo.GetMetricsByRunID(r.Context(), runID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	if m == nil {
		writeError(w, http.StatusNotFound, "metrics not found")
		return
	}
	writeJSON(w, http.StatusOK, m)
}

// handleListRuns serves GET /api/v1/jobs. Despite the name, it returns the
// unified feed of single benchmark runs + test-suite runs introduced by
// PRD-36. Response shape is { rows, total } so the UI can render a
// "showing X-Y of Z" indicator without a second query.
func (s *Server) handleListRuns(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := database.JobFilter{
		Type:   q.Get("type"),
		Status: q.Get("status"),
		Model:  q.Get("model"),
		Sort:   q.Get("sort"),
		Order:  q.Get("order"),
	}
	if v := q.Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			f.Limit = n
		}
	}
	if v := q.Get("offset"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			f.Offset = n
		}
	}

	items, total, err := s.repo.ListJobs(r.Context(), f)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list jobs failed")
		return
	}
	if items == nil {
		items = []database.Job{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"rows":  items,
		"total": total,
	})
}

// handleCancelRun sets cancel_requested on whichever table holds the id and
// best-effort invokes the local cancel function. The owning pod's goroutine
// (which may be this pod or a sibling) picks up the DB flag within 5s via
// its cancel poller and drives the normal teardown path — that's what
// writes the terminal "failed" status. So this handler responds with 202
// "cancelling" rather than "failed" to reflect the asynchronous reality
// (PRD-40).
func (s *Server) handleCancelRun(w http.ResponseWriter, r *http.Request) {
	runID := r.PathValue("id")
	ctx := r.Context()

	// Terminal-state precheck. Look at whichever table holds the id.
	run, err := s.repo.GetBenchmarkRun(ctx, runID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	if run != nil {
		if run.Status != "pending" && run.Status != "running" {
			writeError(w, http.StatusConflict, fmt.Sprintf("cannot cancel run with status %q", run.Status))
			return
		}
	} else {
		suiteRun, err := s.repo.GetTestSuiteRun(ctx, runID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "query failed")
			return
		}
		if suiteRun == nil {
			writeError(w, http.StatusNotFound, "run not found")
			return
		}
		if suiteRun.Status != "pending" && suiteRun.Status != "running" {
			writeError(w, http.StatusConflict, fmt.Sprintf("cannot cancel suite run with status %q", suiteRun.Status))
			return
		}
	}

	// Set the DB flag — the owning pod's poller will see it and cancel.
	if err := s.repo.RequestCancel(ctx, runID); err != nil {
		writeError(w, http.StatusInternalServerError, "request cancel: "+err.Error())
		return
	}
	// Fast-path: if we happen to be the owning pod, short-circuit the 5s poll.
	s.orch.CancelRun(runID)

	writeJSON(w, http.StatusAccepted, map[string]string{
		"id":     runID,
		"status": "cancelling",
	})
}

func (s *Server) handleDeleteRun(w http.ResponseWriter, r *http.Request) {
	runID := r.PathValue("id")
	ctx := r.Context()

	// Try benchmark run first
	run, err := s.repo.GetBenchmarkRun(ctx, runID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}

	if run != nil {
		// Cancel if still active
		if run.Status == "pending" || run.Status == "running" {
			s.orch.CancelRun(runID)
			_ = s.repo.UpdateRunStatus(ctx, runID, "failed")
		}
		if err := s.repo.DeleteRun(ctx, runID); err != nil {
			writeError(w, http.StatusInternalServerError, "delete failed")
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}

	// Try suite run
	suiteRun, err := s.repo.GetTestSuiteRun(ctx, runID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	if suiteRun == nil {
		writeError(w, http.StatusNotFound, "run not found")
		return
	}

	// Cancel if still active
	if suiteRun.Status == "pending" || suiteRun.Status == "running" {
		s.orch.CancelRun(runID)
		_ = s.repo.UpdateSuiteRunStatus(ctx, runID, "failed", nil)
	}
	if err := s.repo.DeleteSuiteRun(ctx, runID); err != nil {
		writeError(w, http.StatusInternalServerError, "delete failed")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleRecommend(w http.ResponseWriter, r *http.Request) {
	modelID := r.URL.Query().Get("model")
	instanceName := r.URL.Query().Get("instance_type")
	if modelID == "" || instanceName == "" {
		writeError(w, http.StatusBadRequest, "model and instance_type query parameters are required")
		return
	}

	hfToken := r.Header.Get("X-HF-Token")

	// Optional overrides from user
	var opts recommend.RecommendOptions
	if tpStr := r.URL.Query().Get("tp"); tpStr != "" {
		fmt.Sscanf(tpStr, "%d", &opts.TPOverride)
	}
	if overheadStr := r.URL.Query().Get("overhead_gib"); overheadStr != "" {
		fmt.Sscanf(overheadStr, "%f", &opts.OverheadGiB)
	}
	if maxMLStr := r.URL.Query().Get("max_model_len"); maxMLStr != "" {
		fmt.Sscanf(maxMLStr, "%d", &opts.MaxModelLenOverride)
	}
	// Make the transformers-compat warning reflect the configured vLLM tag.
	if tv, err := s.repo.GetToolVersions(r.Context()); err == nil && tv != nil {
		opts.VLLMVersion = tv.FrameworkVersion
	}

	// If the model is cached in S3, the run will use the Run:ai streamer,
	// which keeps host RAM close to a small layer buffer. Informs the
	// recommender's host-memory check (host-RAM peak is ~10% of weight
	// size instead of ~130%).
	if mc, _ := s.repo.GetModelCacheByHfID(r.Context(), modelID, "main"); mc != nil && mc.Status == "cached" {
		opts.UseS3Streamer = true
	}

	// PRD-47 PR #5: pass observed per-family host-memory ratios into
	// the recommender so the host-memory check uses empirical data
	// when available. Unseen families keep the conservative default.
	// Non-fatal on query failure.
	if calib, err := s.repo.GetHostMemCalibration(r.Context()); err == nil {
		opts.HostMemCalibration = calib
	} else {
		log.Printf("recommend: host mem calibration query failed: %v", err)
	}
	// opts.ModelType is set below, after FetchModelConfig, so we use
	// HF's canonical architecture name instead of a substring heuristic.

	// Look up instance type from DB.
	instType, err := s.repo.GetInstanceTypeByName(r.Context(), instanceName)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "instance type lookup failed")
		return
	}
	if instType == nil {
		writeError(w, http.StatusNotFound, fmt.Sprintf("instance type %s not found", instanceName))
		return
	}

	// Fetch model config (from S3 cache if available, else HuggingFace).
	modelCfg, err := s.FetchModelConfig(r.Context(), modelID, hfToken)
	if err != nil {
		var hfErr *recommend.HFError
		if errors.As(err, &hfErr) {
			writeError(w, hfErr.StatusCode, hfErr.Message)
			return
		}
		writeError(w, http.StatusBadGateway, "failed to fetch model metadata from HuggingFace")
		return
	}

	// PRD-47 PR #5 depends on parameter_count AND model_type being
	// populated on the models row so the calibration query can derive
	// a weight-size denominator and a per-architecture group key. This
	// is our only reliable hook for it — the create-run path doesn't
	// have the config. Ensure the model exists and write both fields
	// if they're missing.
	if modelCfg != nil {
		if m, err := s.repo.EnsureModel(r.Context(), modelID, "main"); err == nil && m != nil {
			if modelCfg.ParameterCount > 0 {
				if err := s.repo.SetModelParameterCount(r.Context(), m.ID, modelCfg.ParameterCount); err != nil {
					log.Printf("recommend: set parameter_count: %v", err)
				}
			}
			if modelCfg.ModelType != "" {
				if err := s.repo.SetModelType(r.Context(), m.ID, modelCfg.ModelType); err != nil {
					log.Printf("recommend: set model_type: %v", err)
				}
			}
		}
	}
	// Calibration key = HF model_type. Drives which bucket the
	// recommender's host-memory check uses when it runs below.
	if modelCfg != nil {
		opts.ModelType = modelCfg.ModelType
	}

	// Get all GPU instances for suggesting alternatives.
	allInstTypes, err := s.repo.ListInstanceTypes(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list instance types failed")
		return
	}
	var allSpecs []recommend.InstanceSpec
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

	inst := recommend.InstanceSpec{
		Name:                 instType.Name,
		AcceleratorType:      instType.AcceleratorType,
		AcceleratorName:      instType.AcceleratorName,
		AcceleratorCount:     instType.AcceleratorCount,
		AcceleratorMemoryGiB: instType.AcceleratorMemoryGiB,
		MemoryGiB:            instType.MemoryGiB,
	}

	var rec *recommend.Recommendation
	if strings.EqualFold(instType.AcceleratorType, "neuron") {
		rec = recommend.RecommendNeuron(*modelCfg, inst)
	} else {
		rec = recommend.Recommend(*modelCfg, inst, allSpecs, opts)
	}

	// Add valid TP options for UI dropdown
	type responseWithOptions struct {
		*recommend.Recommendation
		ValidTPOptions []int `json:"valid_tp_options,omitempty"`
	}
	resp := responseWithOptions{
		Recommendation: rec,
		ValidTPOptions: recommend.ValidTPOptions(modelCfg.NumAttentionHeads, modelCfg.NumKeyValueHeads, instType.AcceleratorCount),
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleListInstanceTypes(w http.ResponseWriter, r *http.Request) {
	const cacheKey = "instance-types"
	if data := s.cache.Get(cacheKey); data != nil {
		serveCacheHit(w, data)
		return
	}
	types, err := s.repo.ListInstanceTypes(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list instance types failed")
		return
	}
	if types == nil {
		types = []database.InstanceType{}
	}
	s.writeCachedJSON(w, cacheKey, http.StatusOK, types)
}

func (s *Server) handleListPricing(w http.ResponseWriter, r *http.Request) {
	region := r.URL.Query().Get("region")
	if region == "" {
		region = "us-east-2"
	}
	cacheKey := "pricing:" + region
	if data := s.cache.Get(cacheKey); data != nil {
		serveCacheHit(w, data)
		return
	}
	rows, err := s.repo.ListPricing(r.Context(), region)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "pricing query failed")
		return
	}
	if rows == nil {
		rows = []database.PricingRow{}
	}
	s.writeCachedJSON(w, cacheKey, http.StatusOK, rows)
}

// handleCatalogSeed launches the in-process seeder (PRD-30). Replaces the
// previous K8s Job + bash script implementation.
func (s *Server) handleCatalogSeed(w http.ResponseWriter, r *http.Request) {
	if s.seeder == nil {
		writeError(w, http.StatusInternalServerError, "seeder not configured")
		return
	}
	dryRun := r.URL.Query().Get("dry_run") == "true"

	// PRD-31: resolve the platform HF token so gated models in the matrix
	// can be processed without the operator pasting a token per-seed.
	var hfToken string
	if s.secrets != nil {
		if tok, err := s.secrets.GetHFToken(r.Context()); err == nil {
			hfToken = tok
		} else {
			log.Printf("resolve platform HF token for seed: %v", err)
		}
	}

	id, err := s.seeder.Start(r.Context(), seed.Options{DryRun: dryRun, HfToken: hfToken})
	if err != nil {
		if errors.Is(err, seed.ErrSeedAlreadyRunning) {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("start seed: %v", err))
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{
		"seed_id": id,
		"status":  "active",
	})
}

// handleCatalogSeedStatus returns the latest seed's progress. Response shape
// is a superset of the old job-based response to keep the UI working.
func (s *Server) handleCatalogSeedStatus(w http.ResponseWriter, r *http.Request) {
	st, err := s.repo.GetLatestCatalogSeedStatus(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query seed status")
		return
	}
	if st == nil {
		writeJSON(w, http.StatusOK, map[string]string{"status": "none"})
		return
	}
	resp := map[string]any{
		"seed_id":    st.ID,
		"status":     st.Status,
		"total":      st.Total,
		"completed":  st.Completed,
		"dry_run":    st.DryRun,
		"started_at": st.StartedAt.Format(time.RFC3339),
	}
	if st.ErrorMessage != nil {
		resp["error_message"] = *st.ErrorMessage
	}
	if st.CompletedAt != nil {
		resp["completed_at"] = st.CompletedAt.Format(time.RFC3339)
	}
	writeJSON(w, http.StatusOK, resp)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

// handleListScenarios returns all available benchmark scenarios.
func (s *Server) handleListScenarios(w http.ResponseWriter, r *http.Request) {
	const cacheKey = "scenarios"
	if data := s.cache.Get(cacheKey); data != nil {
		serveCacheHit(w, data)
		return
	}
	scenarios := scenario.List()

	// Build response with computed duration
	type scenarioResponse struct {
		ID              string               `json:"id"`
		Name            string               `json:"name"`
		Description     string               `json:"description"`
		DurationSeconds int                  `json:"duration_seconds"`
		LoadType        string               `json:"load_type"`
		Stages          []scenario.LoadStage `json:"stages"`
	}

	result := make([]scenarioResponse, 0, len(scenarios))
	for _, sc := range scenarios {
		result = append(result, scenarioResponse{
			ID:              sc.ID,
			Name:            sc.Name,
			Description:     sc.Description,
			DurationSeconds: sc.TotalDuration(),
			LoadType:        sc.LoadType,
			Stages:          sc.Stages,
		})
	}

	s.writeCachedJSON(w, cacheKey, http.StatusOK, result)
}

// handleListTestSuites returns all available test suites.
func (s *Server) handleListTestSuites(w http.ResponseWriter, r *http.Request) {
	const cacheKey = "test-suites"
	if data := s.cache.Get(cacheKey); data != nil {
		serveCacheHit(w, data)
		return
	}
	suites := testsuite.List()

	type suiteResponse struct {
		ID              string   `json:"id"`
		Name            string   `json:"name"`
		Description     string   `json:"description"`
		Scenarios       []string `json:"scenarios"`
		TotalDuration   int      `json:"total_duration_seconds"`
	}

	result := make([]suiteResponse, 0, len(suites))
	for _, suite := range suites {
		result = append(result, suiteResponse{
			ID:            suite.ID,
			Name:          suite.Name,
			Description:   suite.Description,
			Scenarios:     suite.Scenarios,
			TotalDuration: suite.TotalDuration,
		})
	}

	s.writeCachedJSON(w, cacheKey, http.StatusOK, result)
}

// handleListSuiteRuns returns a list of test suite runs.
func (s *Server) handleListSuiteRuns(w http.ResponseWriter, r *http.Request) {
	items, err := s.repo.ListSuiteRunsWithNames(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list suite runs failed")
		return
	}
	if items == nil {
		items = []database.SuiteRunListItem{}
	}
	writeJSON(w, http.StatusOK, items)
}

// handleCreateSuiteRun creates a new test suite run.
func (s *Server) handleCreateSuiteRun(w http.ResponseWriter, r *http.Request) {
	var req database.SuiteRunRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	// For S3-only models, derive model_hf_id from the S3 URI if not provided
	if req.ModelHfID == "" && req.ModelS3URI != "" {
		req.ModelHfID = req.ModelS3URI
	}

	// Validate required fields
	if req.ModelHfID == "" || req.InstanceTypeName == "" {
		writeError(w, http.StatusBadRequest, "model_hf_id (or model_s3_uri) and instance_type_name are required")
		return
	}

	// Need either suite_id or scenario_ids
	if req.SuiteID == "" && len(req.ScenarioIDs) == 0 {
		writeError(w, http.StatusBadRequest, "either suite_id or scenario_ids is required")
		return
	}

	// Determine scenarios to run
	var scenarioIDs []string
	suiteID := req.SuiteID

	if len(req.ScenarioIDs) > 0 {
		// Custom scenario list
		scenarioIDs = req.ScenarioIDs
		suiteID = "custom"
	} else {
		// Predefined suite
		suite := testsuite.Get(req.SuiteID)
		if suite == nil {
			writeError(w, http.StatusBadRequest, "unknown suite: "+req.SuiteID)
			return
		}
		scenarioIDs = suite.Scenarios
	}

	// Validate all scenarios exist
	for _, scenarioID := range scenarioIDs {
		if scenario.Get(scenarioID) == nil {
			writeError(w, http.StatusBadRequest, "unknown scenario: "+scenarioID)
			return
		}
	}

	ctx := r.Context()

	// Ensure model exists
	model, err := s.repo.EnsureModel(ctx, req.ModelHfID, req.ModelHfRevision)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "ensure model: "+err.Error())
		return
	}

	// Get instance type
	instType, err := s.repo.GetInstanceTypeByName(ctx, req.InstanceTypeName)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "get instance type: "+err.Error())
		return
	}
	if instType == nil {
		writeError(w, http.StatusBadRequest, "unknown instance type: "+req.InstanceTypeName)
		return
	}

	// Create suite run record. PRD-41: persist framework/framework_version/model_s3_uri
	// so the manifest export can reconstruct the deployment exactly.
	// PRD-46: also persist max_num_batched_tokens so the suite manifest
	// reproduces runtime vLLM flags byte-for-byte.
	var suiteMnbtPtr *int
	if req.MaxNumBatchedTokens > 0 {
		n := req.MaxNumBatchedTokens
		suiteMnbtPtr = &n
	}
	var suiteKVDtypePtr *string
	if req.KVCacheDtype != "" {
		v := req.KVCacheDtype
		suiteKVDtypePtr = &v
	}
	suiteRun := &database.TestSuiteRun{
		ModelID:              model.ID,
		InstanceTypeID:       instType.ID,
		SuiteID:              suiteID,
		TensorParallelDegree: req.TensorParallelDegree,
		Quantization:         req.Quantization,
		MaxModelLen:          req.MaxModelLen,
		MaxNumBatchedTokens:  suiteMnbtPtr,
		KVCacheDtype:         suiteKVDtypePtr,
		Status:               "pending",
	}
	if req.Framework != "" {
		fw := req.Framework
		suiteRun.Framework = &fw
	}
	if req.FrameworkVersion != "" {
		fv := req.FrameworkVersion
		suiteRun.FrameworkVersion = &fv
	}
	if req.ModelS3URI != "" {
		s3 := req.ModelS3URI
		suiteRun.ModelS3URI = &s3
	}

	suiteRunID, err := s.repo.CreateTestSuiteRun(ctx, suiteRun)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "create suite run: "+err.Error())
		return
	}

	// Create scenario result records for each scenario
	for _, scenarioID := range scenarioIDs {
		result := &database.ScenarioResult{
			SuiteRunID: suiteRunID,
			ScenarioID: scenarioID,
			Status:     "pending",
		}
		if _, err := s.repo.CreateScenarioResult(ctx, result); err != nil {
			writeError(w, http.StatusInternalServerError, "create scenario result: "+err.Error())
			return
		}
	}

	// Update request with resolved scenario IDs for executor
	req.ScenarioIDs = scenarioIDs

	// Start suite execution in background
	go s.orch.ExecuteSuite(context.Background(), suiteRunID, req)

	// Return the created suite run
	suiteRun.ID = suiteRunID
	writeJSON(w, http.StatusAccepted, suiteRun)
}

type suiteScenarioProgress struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}
type suiteProgressInfo struct {
	Completed int                     `json:"completed"`
	Total     int                     `json:"total"`
	Scenarios []suiteScenarioProgress `json:"scenarios"`
}
type suiteScenarioDefinition struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	TargetQPS       int    `json:"target_qps"`
	DurationSeconds int    `json:"duration_seconds"`
	LoadType        string `json:"load_type"`
}
type suiteRunResponse struct {
	*database.TestSuiteRun
	ModelHfID            string                    `json:"model_hf_id,omitempty"`
	InstanceTypeName     string                    `json:"instance_type_name,omitempty"`
	AcceleratorType      string                    `json:"accelerator_type,omitempty"`
	AcceleratorName      string                    `json:"accelerator_name,omitempty"`
	AcceleratorCount     int                       `json:"accelerator_count,omitempty"`
	AcceleratorMemoryGiB int                       `json:"accelerator_memory_gib,omitempty"`
	// PRD-46: computed --max-num-seqs value used when the model was
	// deployed. Not persisted separately (derived from the busiest
	// scenario's NumWorkers), so we compute it on read for display.
	MaxNumSeqs           int                       `json:"max_num_seqs,omitempty"`
	Progress             suiteProgressInfo         `json:"progress"`
	Results              []database.ScenarioResult `json:"results"`
	ScenarioDefinitions  []suiteScenarioDefinition `json:"scenario_definitions"`
	Instance             *database.InstanceType    `json:"instance,omitempty"`
	Pricing              *database.PricingRow      `json:"pricing,omitempty"`
	Errors               map[string]string         `json:"errors,omitempty"`
}

// handleGetSuiteRun returns a test suite run with its scenario results.
func (s *Server) handleGetSuiteRun(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "missing suite run ID")
		return
	}

	ctx := r.Context()

	suiteRun, err := s.repo.GetTestSuiteRun(ctx, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "get suite run: "+err.Error())
		return
	}
	if suiteRun == nil {
		writeError(w, http.StatusNotFound, "suite run not found")
		return
	}

	results, err := s.repo.GetScenarioResults(ctx, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "get scenario results: "+err.Error())
		return
	}

	completed := 0
	scenarios := make([]suiteScenarioProgress, 0, len(results))
	for _, r := range results {
		scenarios = append(scenarios, suiteScenarioProgress{ID: r.ScenarioID, Status: r.Status})
		if r.Status == "completed" || r.Status == "failed" || r.Status == "skipped" {
			completed++
		}
	}

	scenarioDefs := make([]suiteScenarioDefinition, 0, len(results))
	maxNumSeqs := 0
	for _, r := range results {
		if sc := scenario.Get(r.ScenarioID); sc != nil {
			if ov, _ := s.repo.GetScenarioOverride(ctx, r.ScenarioID); ov != nil {
				sc = sc.Merge(&scenario.Override{
					NumWorkers: ov.NumWorkers,
					Streaming:  ov.Streaming,
					InputMean:  ov.InputMean,
					OutputMean: ov.OutputMean,
				})
			}
			if sc.NumWorkers > maxNumSeqs {
				maxNumSeqs = sc.NumWorkers
			}
			scenarioDefs = append(scenarioDefs, suiteScenarioDefinition{
				ID:              sc.ID,
				Name:            sc.Name,
				TargetQPS:       sc.TargetQPS(),
				DurationSeconds: sc.TotalDuration(),
				LoadType:        sc.LoadType,
			})
		}
	}

	resp := suiteRunResponse{
		TestSuiteRun: suiteRun,
		MaxNumSeqs:   maxNumSeqs,
		Progress: suiteProgressInfo{
			Completed: completed,
			Total:     len(results),
			Scenarios: scenarios,
		},
		Results:             results,
		ScenarioDefinitions: scenarioDefs,
	}
	if model, _ := s.repo.GetModelByID(ctx, suiteRun.ModelID); model != nil {
		resp.ModelHfID = model.HfID
	}
	if it, _ := s.repo.GetInstanceTypeByID(ctx, suiteRun.InstanceTypeID); it != nil {
		resp.InstanceTypeName = it.Name
		resp.AcceleratorType = it.AcceleratorType
		resp.AcceleratorName = it.AcceleratorName
		resp.AcceleratorCount = it.AcceleratorCount
		resp.AcceleratorMemoryGiB = it.AcceleratorMemoryGiB
	}

	includes := parseIncludes(r.URL.Query().Get("include"))
	if includes != nil {
		s.fetchSuiteIncludes(ctx, &resp, includes)
	}

	// ETag only for bare requests on terminal suites (PRD-38).
	if includes == nil && (suiteRun.Status == "completed" || suiteRun.Status == "failed") {
		data, err := json.Marshal(resp)
		if err == nil {
			data = append(data, '\n')
			etag := etagOf(data)
			if checkETag(w, r, etag) {
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write(data)
			return
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) fetchSuiteIncludes(ctx context.Context, resp *suiteRunResponse, includes IncludeSet) {
	var (
		mu sync.Mutex
		g  errgroup.Group
	)
	setErr := func(token, msg string) {
		mu.Lock()
		if resp.Errors == nil {
			resp.Errors = make(map[string]string)
		}
		resp.Errors[token] = msg
		mu.Unlock()
	}

	if includes.Has("instance") {
		g.Go(func() error {
			it, err := s.repo.GetInstanceTypeByID(ctx, resp.InstanceTypeID)
			if err != nil {
				setErr("instance", err.Error())
				return nil
			}
			mu.Lock()
			resp.Instance = it
			mu.Unlock()
			return nil
		})
	}

	if includes.Has("pricing") {
		g.Go(func() error {
			region := os.Getenv("AWS_REGION")
			if region == "" {
				region = "us-east-2"
			}
			p, err := s.repo.GetPricingForInstanceType(ctx, resp.InstanceTypeID, region)
			if err != nil {
				setErr("pricing", err.Error())
				return nil
			}
			if p != nil {
				mu.Lock()
				resp.Pricing = &database.PricingRow{
					InstanceTypeName:     resp.InstanceTypeName,
					OnDemandHourlyUSD:    p.OnDemandHourlyUSD,
					Reserved1YrHourlyUSD: p.Reserved1YrHourlyUSD,
					Reserved3YrHourlyUSD: p.Reserved3YrHourlyUSD,
					EffectiveDate:        p.EffectiveDate,
				}
				mu.Unlock()
			}
			return nil
		})
	}

	g.Wait()

	if len(resp.Errors) == 0 {
		resp.Errors = nil
	}
}


