package database

import (
	"context"
	"time"
)

// Repo defines the interface for benchmark data operations.
// The concrete *Repository satisfies this interface. Use this interface
// as a dependency in consumers to enable testing with mocks.
type Repo interface {
	GetModelByHfID(ctx context.Context, hfID, hfRevision string) (*Model, error)
	EnsureModel(ctx context.Context, hfID, hfRevision string) (*Model, error)
	GetInstanceTypeByName(ctx context.Context, name string) (*InstanceType, error)
	CreateBenchmarkRun(ctx context.Context, run *BenchmarkRun) (string, error)
	UpdateRunStatus(ctx context.Context, runID, status string) error
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

// Compile-time check that *Repository implements Repo.
var _ Repo = (*Repository)(nil)
