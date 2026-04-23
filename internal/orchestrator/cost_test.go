package orchestrator

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/accelbench/accelbench/internal/database"

	"k8s.io/client-go/kubernetes/fake"
)

// seedRunForCost sets up a MockRepo with one completed run that has valid
// started_at / completed_at timestamps and a matching pricing row.
func seedRunForCost(t *testing.T, nodeSec float64, loadgenSec *float64, withPricing bool) (*database.MockRepo, string) {
	t.Helper()
	repo := database.NewMockRepo()

	instID := "inst-001"
	repo.SeedInstanceType(&database.InstanceType{ID: instID, Name: "g5.xlarge"})

	if withPricing {
		_ = repo.UpsertPricing(context.Background(), &database.Pricing{
			ID:                "p1",
			InstanceTypeID:    instID,
			Region:            "us-east-2",
			OnDemandHourlyUSD: 1.0, // $1/hr makes the math obvious
			EffectiveDate:     "2026-01-01",
			CreatedAt:         time.Now(),
		})
	}

	started := time.Now().Add(-time.Duration(nodeSec) * time.Second)
	completed := time.Now()
	runID := "run-001"
	repo.SeedRun(&database.BenchmarkRun{
		ID:             runID,
		InstanceTypeID: instID,
		Status:         "completed",
		StartedAt:      &started,
		CompletedAt:    &completed,
		CreatedAt:      started,
	})

	if loadgenSec != nil {
		_ = repo.PersistMetrics(context.Background(), runID, &database.BenchmarkMetrics{
			TotalDurationSeconds: loadgenSec,
		})
	}
	return repo, runID
}

func TestComputeRunCost_HappyPath(t *testing.T) {
	t.Setenv("AWS_REGION", "us-east-2")
	loadgen := 1800.0 // 30min
	repo, runID := seedRunForCost(t, 3600.0, &loadgen, true) // 1hr node lifetime

	o := New(fake.NewSimpleClientset(), repo, "test-pod")
	total, loadgenCost := o.computeRunCost(context.Background(), runID)

	if total == nil || *total < 0.99 || *total > 1.01 {
		t.Errorf("total = %v, want ~$1.00", total)
	}
	if loadgenCost == nil || *loadgenCost < 0.49 || *loadgenCost > 0.51 {
		t.Errorf("loadgen = %v, want ~$0.50", loadgenCost)
	}
}

func TestComputeRunCost_MissingPricing(t *testing.T) {
	t.Setenv("AWS_REGION", "us-east-2")
	loadgen := 60.0
	repo, runID := seedRunForCost(t, 600.0, &loadgen, false) // no pricing

	o := New(fake.NewSimpleClientset(), repo, "test-pod")
	total, loadgenCost := o.computeRunCost(context.Background(), runID)
	if total != nil || loadgenCost != nil {
		t.Errorf("expected (nil, nil) on missing pricing, got total=%v loadgen=%v", total, loadgenCost)
	}
}

func TestComputeRunCost_FailedRunWithoutMetrics(t *testing.T) {
	t.Setenv("AWS_REGION", "us-east-2")
	// nodeSec > 0, but loadgenSec is nil → no metrics row persisted.
	repo, runID := seedRunForCost(t, 1800.0, nil, true)

	o := New(fake.NewSimpleClientset(), repo, "test-pod")
	total, loadgenCost := o.computeRunCost(context.Background(), runID)

	// Total cost still computed from node window — the node existed, it's billable.
	if total == nil || *total < 0.49 || *total > 0.51 {
		t.Errorf("total = %v, want ~$0.50 (1.0/hr × 0.5hr)", total)
	}
	// Loadgen cost nil because no metrics row.
	if loadgenCost != nil {
		t.Errorf("expected loadgen nil when metrics absent, got %v", loadgenCost)
	}
}

func TestComputeRunCost_NoCompletedAt(t *testing.T) {
	// In-flight run — started_at set, completed_at nil. Cost hook should
	// short-circuit to (nil, nil) rather than compute on a negative duration.
	repo := database.NewMockRepo()
	repo.SeedInstanceType(&database.InstanceType{ID: "inst-001", Name: "g5.xlarge"})
	started := time.Now().Add(-60 * time.Second)
	repo.SeedRun(&database.BenchmarkRun{
		ID: "r", InstanceTypeID: "inst-001", Status: "running",
		StartedAt: &started, CreatedAt: started,
	})
	os.Unsetenv("AWS_REGION")

	o := New(fake.NewSimpleClientset(), repo, "test-pod")
	total, loadgenCost := o.computeRunCost(context.Background(), "r")
	if total != nil || loadgenCost != nil {
		t.Errorf("expected (nil, nil) on in-flight run, got total=%v loadgen=%v", total, loadgenCost)
	}
}
