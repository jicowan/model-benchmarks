package database

import "context"

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
	DeleteRun(ctx context.Context, runID string) error
	UpsertPricing(ctx context.Context, p *Pricing) error
	ListPricing(ctx context.Context, region string) ([]PricingRow, error)
	ListInstanceTypes(ctx context.Context) ([]InstanceType, error)
}

// Compile-time check that *Repository implements Repo.
var _ Repo = (*Repository)(nil)
