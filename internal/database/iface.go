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
	ListCatalog(ctx context.Context, f CatalogFilter) ([]CatalogEntry, error)
	ListRuns(ctx context.Context, f RunFilter) ([]RunListItem, error)
	GetRunsByStatus(ctx context.Context, status string) ([]BenchmarkRun, error)
	DeleteRun(ctx context.Context, runID string) error
	GetRunExportDetails(ctx context.Context, runID string) (*RunExportDetails, error)
	UpsertPricing(ctx context.Context, p *Pricing) error
	ListPricing(ctx context.Context, region string) ([]PricingRow, error)
	ListInstanceTypes(ctx context.Context) ([]InstanceType, error)
	// OOM event tracking
	OOMRepo
	// Test suite operations
	TestSuiteRepo
	// Model cache operations
	ModelCacheRepo
	// Catalog seeding matrix (PRD-30)
	CatalogMatrixRepo
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
	ListModelCache(ctx context.Context) ([]ModelCache, error)
	UpdateModelCacheStatus(ctx context.Context, id, status string, errMsg *string) error
	UpdateModelCacheComplete(ctx context.Context, id string, sizeBytes int64) error
	DeleteModelCache(ctx context.Context, id string) error
}

// Compile-time check that *Repository implements Repo.
var _ Repo = (*Repository)(nil)
