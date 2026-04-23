package database

import (
	"context"
	"testing"
	"time"
)

// mockRepoWithStats seeds a MockRepo with a realistic mix of runs/suites/
// model_cache entries for the Dashboard aggregate tests. Returns pointers
// where the test needs to tweak state (cost values).
func mockRepoWithStats(t *testing.T) *MockRepo {
	t.Helper()
	r := NewMockRepo()

	// 3 completed single runs with cost.
	for i, cost := range []float64{1.23, 2.50, 0.75} {
		c := cost
		r.runs[string(rune('a'+i))] = &BenchmarkRun{
			ID:           string(rune('a' + i)),
			Status:       "completed",
			TotalCostUSD: &c,
			CreatedAt:    time.Now().UTC().Add(-24 * time.Hour),
		}
	}
	// 1 failed single run without cost (missing pricing path).
	r.runs["f"] = &BenchmarkRun{ID: "f", Status: "failed", CreatedAt: time.Now()}
	// 2 running — should not affect success denominator.
	r.runs["r1"] = &BenchmarkRun{ID: "r1", Status: "running", CreatedAt: time.Now()}
	r.runs["r2"] = &BenchmarkRun{ID: "r2", Status: "pending", CreatedAt: time.Now()}

	// 1 completed suite with cost.
	suiteCost := 12.00
	r.suiteRuns["s1"] = &TestSuiteRun{
		ID: "s1", Status: "completed", TotalCostUSD: &suiteCost, CreatedAt: time.Now(),
	}
	// 1 running suite — contributes to total_suites and active_count but
	// not to completed_count or total_cost_usd.
	r.suiteRuns["s2"] = &TestSuiteRun{ID: "s2", Status: "running", CreatedAt: time.Now()}

	// 2 cached models, 1 pending.
	r.modelCache["m1"] = &ModelCache{ID: "m1", Status: "cached"}
	r.modelCache["m2"] = &ModelCache{ID: "m2", Status: "cached"}
	r.modelCache["m3"] = &ModelCache{ID: "m3", Status: "pending"}

	return r
}

func TestDashboardStats_Counts(t *testing.T) {
	repo := mockRepoWithStats(t)
	stats, err := repo.DashboardStats(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	if stats.TotalSingle != 6 {
		t.Errorf("total_single = %d, want 6", stats.TotalSingle)
	}
	if stats.TotalSuites != 2 {
		t.Errorf("total_suites = %d, want 2", stats.TotalSuites)
	}
	if stats.TotalRuns != 8 {
		t.Errorf("total_runs = %d, want 8", stats.TotalRuns)
	}
	// pending + running across both tables: 2 singles + 1 suite = 3
	if stats.ActiveCount != 3 {
		t.Errorf("active_count = %d, want 3", stats.ActiveCount)
	}
	// completed across both tables: 3 singles + 1 suite = 4
	if stats.CompletedCount != 4 {
		t.Errorf("completed_count = %d, want 4", stats.CompletedCount)
	}
	if stats.FailedCount != 1 {
		t.Errorf("failed_count = %d, want 1", stats.FailedCount)
	}
	if stats.CachedModels != 2 {
		t.Errorf("cached_models = %d, want 2", stats.CachedModels)
	}
}

// TestDashboardStats_CostSumsBothTables ensures we don't double-count or
// miss cost from either benchmark_runs or test_suite_runs.
func TestDashboardStats_CostSumsBothTables(t *testing.T) {
	repo := mockRepoWithStats(t)
	stats, err := repo.DashboardStats(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// 1.23 + 2.50 + 0.75 + 12.00 = 16.48
	want := 16.48
	if stats.TotalCostUSD < want-0.001 || stats.TotalCostUSD > want+0.001 {
		t.Errorf("total_cost_usd = %.4f, want %.4f", stats.TotalCostUSD, want)
	}
}

// TestDashboardStats_CostPerDayHas14Buckets verifies the per-day series is
// always exactly 14 entries, zero-filled on days with no runs.
func TestDashboardStats_CostPerDayHas14Buckets(t *testing.T) {
	repo := mockRepoWithStats(t)
	stats, err := repo.DashboardStats(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(stats.CostPerDay) != 14 {
		t.Errorf("len(cost_per_day) = %d, want 14", len(stats.CostPerDay))
	}
	// Every entry has a non-empty day label and a non-negative cost.
	for _, d := range stats.CostPerDay {
		if d.Day == "" {
			t.Errorf("empty day in cost_per_day")
		}
		if d.CostUSD < 0 {
			t.Errorf("negative cost %.4f on day %s", d.CostUSD, d.Day)
		}
	}
}

// TestDashboardStats_EmptyRepo handles the no-runs case — specifically that
// we don't divide by zero anywhere and all fields are zeroed.
func TestDashboardStats_EmptyRepo(t *testing.T) {
	repo := NewMockRepo()
	stats, err := repo.DashboardStats(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if stats.TotalRuns != 0 || stats.TotalCostUSD != 0 {
		t.Errorf("expected empty stats, got %+v", stats)
	}
	if len(stats.CostPerDay) != 14 {
		t.Errorf("cost_per_day should still be 14 zero-filled buckets on empty repo")
	}
}

// TestModelCacheStats_Filters ensures the caching bucket includes both
// 'caching' and 'pending' statuses and TotalBytes only sums cached rows.
func TestModelCacheStats_Filters(t *testing.T) {
	repo := NewMockRepo()
	size1 := int64(100)
	size2 := int64(200)
	sizeBad := int64(9999) // attached to a failed row; should not count.
	repo.modelCache["a"] = &ModelCache{ID: "a", Status: "cached", SizeBytes: &size1}
	repo.modelCache["b"] = &ModelCache{ID: "b", Status: "cached", SizeBytes: &size2}
	repo.modelCache["c"] = &ModelCache{ID: "c", Status: "caching"}
	repo.modelCache["d"] = &ModelCache{ID: "d", Status: "pending"}
	repo.modelCache["e"] = &ModelCache{ID: "e", Status: "failed", SizeBytes: &sizeBad}

	s, err := repo.ModelCacheStats(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if s.Total != 5 {
		t.Errorf("total = %d, want 5", s.Total)
	}
	if s.Cached != 2 {
		t.Errorf("cached = %d, want 2", s.Cached)
	}
	if s.Caching != 2 {
		t.Errorf("caching (incl. pending) = %d, want 2", s.Caching)
	}
	if s.Failed != 1 {
		t.Errorf("failed = %d, want 1", s.Failed)
	}
	if s.TotalBytes != 300 {
		t.Errorf("total_bytes = %d, want 300 (100+200; failed row excluded)", s.TotalBytes)
	}
}
