package orchestrator

import (
	"math"
	"testing"
)

func approx(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func ptrOf(v float64) *float64 { return &v }

// TestAggregateGPU_SingleShardReproducesLegacy is the load-bearing regression
// guard (PRD-59 Goal 6): with one shard, the roll-up must equal the exact
// numbers the pre-PRD-59 GPUScraper.Stop() produced. Legacy math:
//   util avg = mean, peak = max; mem peak = max(MiB)/1024, avg = mean(MiB)/1024;
//   SM/tensor/DRAM avg/peak = mean/max (nil if empty).
func TestAggregateGPU_SingleShardReproducesLegacy(t *testing.T) {
	util := []float64{40, 60, 80}       // mean 60, peak 80
	mem := []float64{10240, 20480}      // MiB → 10 GiB, 20 GiB; peak 20, avg 15
	sm := []float64{50, 70}             // mean 60, peak 70
	key := gpuShardKey{Node: "node-0"} // role empty (single/co-located)
	samples := map[gpuShardKey]*gpuShardSamples{
		key: {util: util, memMiB: mem, sm: sm},
	}
	agg := aggregateGPU(samples, []gpuShardKey{key})

	if !approx(agg.UtilizationAvgPct, 60) || !approx(agg.UtilizationPeakPct, 80) {
		t.Errorf("util avg/peak = %.3f/%.3f, want 60/80", agg.UtilizationAvgPct, agg.UtilizationPeakPct)
	}
	if !approx(agg.MemoryPeakGiB, 20) || !approx(agg.MemoryAvgGiB, 15) {
		t.Errorf("mem peak/avg = %.3f/%.3f GiB, want 20/15", agg.MemoryPeakGiB, agg.MemoryAvgGiB)
	}
	// Single shard: total == that shard's peak.
	if !approx(agg.MemoryTotalGiB, 20) {
		t.Errorf("mem total = %.3f, want 20 (single shard = its peak)", agg.MemoryTotalGiB)
	}
	if agg.SMActiveAvgPct == nil || !approx(*agg.SMActiveAvgPct, 60) ||
		agg.SMActivePeakPct == nil || !approx(*agg.SMActivePeakPct, 70) {
		t.Errorf("SM avg/peak wrong: %v/%v", agg.SMActiveAvgPct, agg.SMActivePeakPct)
	}
	// No tensor/DRAM samples → nil (NULL), matching aggregatePctSamples.
	if agg.TensorActiveAvgPct != nil || agg.DRAMActiveAvgPct != nil {
		t.Errorf("absent DCP metrics should be nil, got tensor=%v dram=%v", agg.TensorActiveAvgPct, agg.DRAMActiveAvgPct)
	}
	// One shard in the breakdown.
	if len(agg.Shards) != 1 || agg.Shards[0].Node != "node-0" {
		t.Fatalf("expected 1 shard node-0, got %+v", agg.Shards)
	}
}

// TestAggregateGPU_MultiNodeMemorySumVsPeak verifies the core PRD-59 correctness
// fix: memory TOTAL sums per-node peaks (the honest group memory), while memory
// PEAK stays the hottest single node reading (unchanged column meaning), and
// utilization is a group mean/peak (not summed).
func TestAggregateGPU_MultiNodeMemorySumVsPeak(t *testing.T) {
	prefill := gpuShardKey{Node: "node-0", Role: "prefill"}
	decode := gpuShardKey{Node: "node-1", Role: "decode"}
	samples := map[gpuShardKey]*gpuShardSamples{
		// prefill: util mean 80/peak 90; mem peak 20 GiB
		prefill: {util: []float64{70, 90}, memMiB: []float64{20480}},
		// decode: util mean 20/peak 30; mem peak 10 GiB
		decode: {util: []float64{10, 30}, memMiB: []float64{10240}},
	}
	agg := aggregateGPU(samples, []gpuShardKey{prefill, decode})

	// Memory TOTAL = 20 + 10 = 30 GiB (sum of per-node peaks) — the honest
	// "GPU memory the whole 2-node deployment used".
	if !approx(agg.MemoryTotalGiB, 30) {
		t.Errorf("mem total = %.3f, want 30 (sum of per-node peaks)", agg.MemoryTotalGiB)
	}
	// Memory PEAK = 20 GiB (hottest single node) — NOT the sum. This is the old
	// column's meaning, preserved.
	if !approx(agg.MemoryPeakGiB, 20) {
		t.Errorf("mem peak = %.3f, want 20 (hottest node, not summed)", agg.MemoryPeakGiB)
	}
	// Utilization is a group mean over all readings (70,90,10,30)=50, peak 90.
	if !approx(agg.UtilizationAvgPct, 50) || !approx(agg.UtilizationPeakPct, 90) {
		t.Errorf("util avg/peak = %.3f/%.3f, want 50/90", agg.UtilizationAvgPct, agg.UtilizationPeakPct)
	}
	// Two shards, each with its own role, in insertion order.
	if len(agg.Shards) != 2 {
		t.Fatalf("want 2 shards, got %d", len(agg.Shards))
	}
	if agg.Shards[0].Role != "prefill" || agg.Shards[1].Role != "decode" {
		t.Errorf("shard order/roles wrong: %s, %s", agg.Shards[0].Role, agg.Shards[1].Role)
	}
	// Per-shard memory peaks are attributed correctly.
	if !approx(agg.Shards[0].MemoryPeakGiB, 20) || !approx(agg.Shards[1].MemoryPeakGiB, 10) {
		t.Errorf("per-shard mem peaks wrong: %.1f, %.1f", agg.Shards[0].MemoryPeakGiB, agg.Shards[1].MemoryPeakGiB)
	}
}

// TestAggregateGPU_BothRoleShardFlowsThrough (PRD-63): the aggregation keys on
// an arbitrary role string, so a co-located "both" shard appears in the
// breakdown with role="both" and contributes to the group totals — no PRD-59
// change was needed to support it.
func TestAggregateGPU_BothRoleShardFlowsThrough(t *testing.T) {
	both0 := gpuShardKey{Node: "node-0", Role: "both"}
	both1 := gpuShardKey{Node: "node-1", Role: "both"}
	prefill := gpuShardKey{Node: "node-2", Role: "prefill"}
	samples := map[gpuShardKey]*gpuShardSamples{
		both0:   {util: []float64{60, 80}, memMiB: []float64{15360}}, // peak 15 GiB
		both1:   {util: []float64{40, 60}, memMiB: []float64{10240}}, // peak 10 GiB
		prefill: {util: []float64{20}, memMiB: []float64{5120}},      // peak 5 GiB
	}
	agg := aggregateGPU(samples, []gpuShardKey{both0, both1, prefill})

	// Three shards; the two both shards carry role="both".
	if len(agg.Shards) != 3 {
		t.Fatalf("want 3 shards, got %d", len(agg.Shards))
	}
	nBoth := 0
	for _, s := range agg.Shards {
		if s.Role == "both" {
			nBoth++
		}
	}
	if nBoth != 2 {
		t.Errorf("expected 2 both-role shards in the breakdown, got %d", nBoth)
	}
	// Memory total sums all per-node peaks: 15 + 10 + 5 = 30 GiB.
	if !approx(agg.MemoryTotalGiB, 30) {
		t.Errorf("mem total = %.3f, want 30 (sum of per-node peaks incl. both)", agg.MemoryTotalGiB)
	}
}

// TestAggregateGPU_Empty returns zeroes/nils without panicking.
func TestAggregateGPU_Empty(t *testing.T) {
	agg := aggregateGPU(map[gpuShardKey]*gpuShardSamples{}, nil)
	if agg.UtilizationAvgPct != 0 || agg.MemoryTotalGiB != 0 || len(agg.Shards) != 0 {
		t.Errorf("empty aggregation should be zero-valued, got %+v", agg)
	}
	if agg.SMActiveAvgPct != nil {
		t.Errorf("empty DCP should be nil")
	}
}
