package database

import (
	"context"
	"time"
)

// Repo defines the interface for benchmark data operations.
// The concrete *Repository satisfies this interface. Use this interface
// as a dependency in consumers to enable testing with mocks.
type Repo interface {
	Ping(ctx context.Context) error
	GetModelByHfID(ctx context.Context, hfID, hfRevision string) (*Model, error)
	GetModelByID(ctx context.Context, id string) (*Model, error)
	EnsureModel(ctx context.Context, hfID, hfRevision string) (*Model, error)
	GetInstanceTypeByName(ctx context.Context, name string) (*InstanceType, error)
	GetInstanceTypeByID(ctx context.Context, id string) (*InstanceType, error)
	CreateBenchmarkRun(ctx context.Context, run *BenchmarkRun) (string, error)
	UpdateRunStatus(ctx context.Context, runID, status string) error
	UpdateRunFailed(ctx context.Context, runID, reason string) error
	UpdateLoadgenConfig(ctx context.Context, runID, config string) error
	SetLoadgenStartedAt(ctx context.Context, runID string) error
	GetLoadgenStartedAt(ctx context.Context, runID string) (*time.Time, error)
	PersistMetrics(ctx context.Context, runID string, m *BenchmarkMetrics) error
	GetBenchmarkRun(ctx context.Context, runID string) (*BenchmarkRun, error)
	GetMetricsByRunID(ctx context.Context, runID string) (*BenchmarkMetrics, error)
	ListCatalog(ctx context.Context, f CatalogFilter) ([]CatalogEntry, int, error)
	ListRuns(ctx context.Context, f RunFilter) ([]RunListItem, error)
	// PRD-36: unified single-run + suite-run feed with pagination + sort.
	ListJobs(ctx context.Context, f JobFilter) ([]Job, int, error)
	GetRunsByStatus(ctx context.Context, status string) ([]BenchmarkRun, error)
	DeleteRun(ctx context.Context, runID string) error
	GetRunExportDetails(ctx context.Context, runID string) (*RunExportDetails, error)
	UpsertPricing(ctx context.Context, p *Pricing) error
	ListPricing(ctx context.Context, region string) ([]PricingRow, error)
	// PRD-35: point lookup used by the orchestrator at run completion.
	GetPricingForInstanceType(ctx context.Context, instanceTypeID, region string) (*Pricing, error)
	ListInstanceTypes(ctx context.Context) ([]InstanceType, error)
	// PRD-35: cost persistence. Suite cost is computed from the suite's own
	// started_at → completed_at span (single shared EC2 node), not a sum of
	// children — scenario_results rows don't reference benchmark_runs.
	UpdateRunCost(ctx context.Context, runID string, totalUSD, loadgenUSD *float64) error
	UpdateSuiteRunCost(ctx context.Context, suiteRunID string, totalUSD *float64) error
	// PRD-35: aggregate endpoints.
	DashboardStats(ctx context.Context) (*DashboardStats, error)
	ModelCacheStats(ctx context.Context) (*ModelCacheStats, error)
	// OOM event tracking
	OOMRepo
	// Test suite operations
	TestSuiteRepo
	// Model cache operations
	ModelCacheRepo
	// Catalog seeding matrix (PRD-30)
	CatalogMatrixRepo
	// Configuration — scenario overrides, audit log, matrix PUT (PRD-32)
	ConfigRepo
	// Tool versions — vLLM + inference-perf singleton (PRD-34)
	ToolVersionsRepo
}

// ToolVersionsRepo exposes the tool_versions singleton row (PRD-34).
type ToolVersionsRepo interface {
	GetToolVersions(ctx context.Context) (*ToolVersions, error)
	PutToolVersions(ctx context.Context, tv *ToolVersions) error
}

// ConfigRepo covers the Configuration-page storage introduced in PRD-32.
type ConfigRepo interface {
	// Atomic PUT of the full seeding matrix. Accepts an expected version
	// (max updated_at across the three tables) and returns ErrStaleVersion
	// if the DB has newer data.
	PutCatalogMatrix(ctx context.Context, m *CatalogMatrix, expectedVersion time.Time) error

	ListScenarioOverrides(ctx context.Context) ([]ScenarioOverride, error)
	GetScenarioOverride(ctx context.Context, scenarioID string) (*ScenarioOverride, error)
	UpsertScenarioOverride(ctx context.Context, o *ScenarioOverride) error
	DeleteScenarioOverride(ctx context.Context, scenarioID string) error

	InsertAuditLog(ctx context.Context, action, summary string, actor *string) error
	ListAuditLog(ctx context.Context, limit int) ([]ConfigAuditEntry, error)
}

// CatalogMatrixRepo defines the interface for the DB-backed seeding matrix
// introduced in PRD-30 (replaces the accelbench-catalog-scripts ConfigMap).
type CatalogMatrixRepo interface {
	// Matrix reads
	LoadCatalogMatrix(ctx context.Context) (*CatalogMatrix, error)
	ModelCacheByHfID(ctx context.Context) (map[string]ModelCache, error)
	// Dedup set for the seeder: all (model_hf_id, instance_type_name) pairs
	// that already have a non-failed benchmark run.
	ListRunKeys(ctx context.Context) ([]RunKey, error)
	// Seed status lifecycle
	CreateCatalogSeedStatus(ctx context.Context, id string, total int, dryRun bool) error
	UpdateCatalogSeedProgress(ctx context.Context, id string, completed int) error
	CompleteCatalogSeedStatus(ctx context.Context, id string) error
	FailCatalogSeedStatus(ctx context.Context, id, errMsg string) error
	InterruptActiveCatalogSeeds(ctx context.Context) error
	GetLatestCatalogSeedStatus(ctx context.Context) (*CatalogSeedStatus, error)
	GetActiveCatalogSeed(ctx context.Context) (*CatalogSeedStatus, error)
}

// OOMRepo defines the interface for OOM event operations.
type OOMRepo interface {
	CreateOOMEvent(ctx context.Context, event *OOMEvent) error
	GetOOMHistory(ctx context.Context, modelHfID, instanceType string, limit int) (*OOMHistory, error)
}

// OOMEvent represents an OOM event record (mirrors oom.Event for database layer).
type OOMEvent struct {
	ID                   string
	RunID                string
	ModelHfID            string
	InstanceType         string
	PodName              string
	ContainerName        string
	DetectionMethod      string
	ExitCode             int
	Message              string
	OccurredAt           time.Time
	CreatedAt            time.Time
	TensorParallelDegree int
	Concurrency          int
	MaxModelLen          int
	Quantization         string
}

// OOMHistory holds OOM events for a model+instance combination.
type OOMHistory struct {
	ModelHfID    string
	InstanceType string
	Events       []OOMEvent
	TotalCount   int
}

// TestSuiteRepo defines the interface for test suite operations.
type TestSuiteRepo interface {
	CreateTestSuiteRun(ctx context.Context, run *TestSuiteRun) (string, error)
	GetTestSuiteRun(ctx context.Context, id string) (*TestSuiteRun, error)
	UpdateSuiteRunStatus(ctx context.Context, id, status string, currentScenario *string) error
	CreateScenarioResult(ctx context.Context, result *ScenarioResult) (string, error)
	UpdateScenarioResult(ctx context.Context, result *ScenarioResult) error
	GetScenarioResults(ctx context.Context, suiteRunID string) ([]ScenarioResult, error)
	ListTestSuiteRuns(ctx context.Context, modelID, instanceTypeID string) ([]TestSuiteRun, error)
	ListSuiteRunsWithNames(ctx context.Context) ([]SuiteRunListItem, error)
	DeleteSuiteRun(ctx context.Context, id string) error
}

// ModelCacheRepo defines the interface for model cache operations.
type ModelCacheRepo interface {
	CreateModelCache(ctx context.Context, m *ModelCache) (string, error)
	GetModelCache(ctx context.Context, id string) (*ModelCache, error)
	GetModelCacheByHfID(ctx context.Context, hfID, revision string) (*ModelCache, error)
	ListModelCache(ctx context.Context, f ModelCacheFilter) ([]ModelCache, int, error)
	UpdateModelCacheStatus(ctx context.Context, id, status string, errMsg *string) error
	UpdateModelCacheComplete(ctx context.Context, id string, sizeBytes int64) error
	DeleteModelCache(ctx context.Context, id string) error
}

// Compile-time check that *Repository implements Repo.
var _ Repo = (*Repository)(nil)
