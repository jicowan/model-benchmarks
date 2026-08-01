package database

import (
	"context"
	"testing"
)

// TestShardMetrics_RoundTrip verifies PRD-59 shard persistence: a distributed
// run's per-node/per-role breakdown persists and reads back, and the honest
// memory-total is carried. Uses MockRepo (the pgx path is covered by the same
// struct contract).
func TestShardMetrics_RoundTrip(t *testing.T) {
	repo := NewMockRepo()
	repo.SeedModel(&Model{ID: "m1", HfID: "Qwen/Qwen2.5-1.5B-Instruct", HfRevision: "main"})
	repo.SeedInstanceType(&InstanceType{ID: "i1", Name: "g6.2xlarge", AcceleratorType: "gpu", AcceleratorCount: 1})
	runID, err := repo.CreateBenchmarkRun(context.Background(), &BenchmarkRun{ModelID: "m1", InstanceTypeID: "i1", Status: "running"})
	if err != nil {
		t.Fatal(err)
	}

	total := 30.0
	pAvg, pPeak := 80.0, 90.0
	repo.PersistMetrics(context.Background(), runID, &BenchmarkMetrics{
		AcceleratorMemoryTotalGiB: &total,
		Shards: []ShardMetric{
			{RunID: runID, Node: "10.0.0.1", Role: "prefill", Samples: 12, UtilizationAvgPct: &pAvg, UtilizationPeakPct: &pPeak},
			{RunID: runID, Node: "10.0.0.2", Role: "decode", Samples: 12},
		},
	})

	m, err := repo.GetMetricsByRunID(context.Background(), runID)
	if err != nil || m == nil {
		t.Fatalf("GetMetricsByRunID: %v", err)
	}
	if m.AcceleratorMemoryTotalGiB == nil || *m.AcceleratorMemoryTotalGiB != 30 {
		t.Errorf("memory total not persisted: %v", m.AcceleratorMemoryTotalGiB)
	}

	shards, err := repo.GetShardMetrics(context.Background(), runID)
	if err != nil {
		t.Fatal(err)
	}
	if len(shards) != 2 {
		t.Fatalf("want 2 shards, got %d", len(shards))
	}
	if shards[0].Role != "prefill" || shards[1].Role != "decode" {
		t.Errorf("shard roles wrong: %s, %s", shards[0].Role, shards[1].Role)
	}
	if shards[0].UtilizationPeakPct == nil || *shards[0].UtilizationPeakPct != 90 {
		t.Errorf("prefill util peak wrong: %v", shards[0].UtilizationPeakPct)
	}
}

// TestShardMetrics_SingleNodeWritesNone guards the regression contract: a
// single-instance run (no Shards on the metrics) persists NO shard rows.
func TestShardMetrics_SingleNodeWritesNone(t *testing.T) {
	repo := NewMockRepo()
	repo.SeedModel(&Model{ID: "m1", HfID: "meta-llama/Llama-3.1-8B", HfRevision: "main"})
	repo.SeedInstanceType(&InstanceType{ID: "i1", Name: "g5.xlarge", AcceleratorType: "gpu", AcceleratorCount: 1})
	runID, _ := repo.CreateBenchmarkRun(context.Background(), &BenchmarkRun{ModelID: "m1", InstanceTypeID: "i1", Status: "running"})

	repo.PersistMetrics(context.Background(), runID, &BenchmarkMetrics{}) // no Shards, no total

	m, _ := repo.GetMetricsByRunID(context.Background(), runID)
	if m.AcceleratorMemoryTotalGiB != nil {
		t.Errorf("single-node should have NULL memory total, got %v", m.AcceleratorMemoryTotalGiB)
	}
	shards, _ := repo.GetShardMetrics(context.Background(), runID)
	if len(shards) != 0 {
		t.Errorf("single-node should write no shard rows, got %d", len(shards))
	}
}

// TestPDMetrics_RoundTrip (PRD-62): the disaggregation/KV/EPP run-level summary
// fields persist and read back; NULL for a run that never sets them.
func TestPDMetrics_RoundTrip(t *testing.T) {
	repo := NewMockRepo()
	repo.SeedModel(&Model{ID: "m1", HfID: "Qwen/Qwen2.5-1.5B-Instruct", HfRevision: "main"})
	repo.SeedInstanceType(&InstanceType{ID: "i1", Name: "g6.2xlarge", AcceleratorType: "gpu", AcceleratorCount: 1})
	runID, _ := repo.CreateBenchmarkRun(context.Background(), &BenchmarkRun{ModelID: "m1", InstanceTypeID: "i1", Status: "running"})

	xfer, bytes, engaged := 4.2, 1048576.0, 70.0
	repo.PersistMetrics(context.Background(), runID, &BenchmarkMetrics{
		KVTransferTimeAvgMs:  &xfer,
		KVTransferBytesTotal: &bytes,
		DisaggEngagedRatePct: &engaged,
	})
	m, err := repo.GetMetricsByRunID(context.Background(), runID)
	if err != nil || m == nil {
		t.Fatalf("GetMetricsByRunID: %v", err)
	}
	if m.KVTransferTimeAvgMs == nil || *m.KVTransferTimeAvgMs != 4.2 {
		t.Errorf("kv_transfer_time not persisted: %v", m.KVTransferTimeAvgMs)
	}
	if m.DisaggEngagedRatePct == nil || *m.DisaggEngagedRatePct != 70 {
		t.Errorf("disagg_engaged_rate not persisted: %v", m.DisaggEngagedRatePct)
	}
	// A field never set stays NULL.
	if m.KVTransferFailures != nil {
		t.Errorf("unset kv_transfer_failures should be NULL, got %v", m.KVTransferFailures)
	}
}
