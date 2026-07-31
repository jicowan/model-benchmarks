package orchestrator

import "github.com/accelbench/accelbench/internal/database"

// PRD-59: the metrics aggregation layer — the ONE place the per-metric
// sum-vs-mean and roll-up-vs-breakdown rules live. Pure functions over keyed
// GPU samples, heavily unit-tested. The scraper feeds it; the persistence and
// report layers read its output.
//
// Design contract (PRD-59 Goals 2 + 6):
//   - utilization / SM / tensor / DRAM active → group MEAN + group PEAK
//     (peak = hottest single node-reading across the group).
//   - GPU memory → group PEAK stays per-node-peak (existing column meaning is
//     preserved — PRD-59 risk note prefers NOT redefining it), group AVG is the
//     mean of all readings, and a NEW group TOTAL = sum of per-node peaks
//     answers "how much GPU memory did the whole deployment use".
//   - A single-shard input reproduces today's exact numbers (regression guard
//     in aggregate_test.go), so single-instance runs are byte-for-byte unchanged.

// gpuShardKey identifies one sampling shard: a serving node, optionally tagged
// with its prefill/decode role. Role is "" for co-located and single-instance
// runs (no role dimension).
type gpuShardKey struct {
	Node string
	Role string
}

// gpuShardSamples accumulates the per-scrape readings for one shard. Memory is
// kept in the raw DCGM unit (MiB) exactly as the pre-PRD-59 scraper stored it,
// so the reduction arithmetic is identical for the single-shard case. util/SM/
// tensor/DRAM are percentages (0–100); util is the within-node mean across GPUs
// and mem is the within-node sum across GPUs, as parseDCGMMetrics already emits.
type gpuShardSamples struct {
	util   []float64
	memMiB []float64
	sm     []float64
	tensor []float64
	dram   []float64
}

// ShardMetrics is the reduced GPU summary for one node/role shard. Pointers for
// the DCP metrics preserve the "no data" (NULL) signal, matching GPUMetrics.
type ShardMetrics struct {
	Node    string `json:"node"`
	Role    string `json:"role,omitempty"`
	Samples int    `json:"samples"`

	UtilizationAvgPct  float64 `json:"utilization_avg_pct"`
	UtilizationPeakPct float64 `json:"utilization_peak_pct"`
	MemoryPeakGiB      float64 `json:"memory_peak_gib"`
	MemoryAvgGiB       float64 `json:"memory_avg_gib"`

	SMActiveAvgPct      *float64 `json:"sm_active_avg_pct,omitempty"`
	SMActivePeakPct     *float64 `json:"sm_active_peak_pct,omitempty"`
	TensorActiveAvgPct  *float64 `json:"tensor_active_avg_pct,omitempty"`
	TensorActivePeakPct *float64 `json:"tensor_active_peak_pct,omitempty"`
	DRAMActiveAvgPct    *float64 `json:"dram_active_avg_pct,omitempty"`
	DRAMActivePeakPct   *float64 `json:"dram_active_peak_pct,omitempty"`
}

// GPUAggregation is the full reduced result: the group roll-up (the flat
// numbers persisted on benchmark_metrics, unchanged for single-node) plus the
// per-shard breakdown (persisted only for distributed runs, PRD-59 Layer 3).
type GPUAggregation struct {
	// Roll-up (group-level). These map onto the existing GPUMetrics fields.
	UtilizationAvgPct  float64
	UtilizationPeakPct float64
	MemoryPeakGiB      float64 // group peak = hottest single node-reading (unchanged meaning)
	MemoryAvgGiB       float64
	MemoryTotalGiB     float64 // NEW (PRD-59): sum of per-node peak memory across the group

	SMActiveAvgPct      *float64
	SMActivePeakPct     *float64
	TensorActiveAvgPct  *float64
	TensorActivePeakPct *float64
	DRAMActiveAvgPct    *float64
	DRAMActivePeakPct   *float64

	// Per-shard breakdown (node/role). Empty for single-instance runs.
	Shards []ShardMetrics
}

const dcgmMiBPerGiB = 1024 // DCGM FB_USED is MiB; the pre-PRD-59 scraper divided by 1024

// meanPeak reduces a scalar sample series to (avg, peak). Empty → (0, 0), the
// same zero the pre-PRD-59 inline loops produced.
func meanPeak(samples []float64) (avg, peak float64) {
	if len(samples) == 0 {
		return 0, 0
	}
	var sum float64
	for _, v := range samples {
		sum += v
		if v > peak {
			peak = v
		}
	}
	return sum / float64(len(samples)), peak
}

// reduceShard reduces one shard's raw samples to a ShardMetrics.
func reduceShard(key gpuShardKey, s *gpuShardSamples) ShardMetrics {
	utilAvg, utilPeak := meanPeak(s.util)
	// Memory: reduce in MiB then convert to GiB, matching the pre-PRD-59 order
	// of operations exactly (per-sample MiB kept; /1024 at the end).
	memAvgMiB, memPeakMiB := meanPeak(s.memMiB)
	smAvg, smPeak := aggregatePctSamples(s.sm)
	tensorAvg, tensorPeak := aggregatePctSamples(s.tensor)
	dramAvg, dramPeak := aggregatePctSamples(s.dram)

	// Samples count = number of util readings (the primary series), matching the
	// scrape cadence; used only for display/debug.
	return ShardMetrics{
		Node:                key.Node,
		Role:                key.Role,
		Samples:             len(s.util),
		UtilizationAvgPct:   utilAvg,
		UtilizationPeakPct:  utilPeak,
		MemoryPeakGiB:       memPeakMiB / dcgmMiBPerGiB,
		MemoryAvgGiB:        memAvgMiB / dcgmMiBPerGiB,
		SMActiveAvgPct:      smAvg,
		SMActivePeakPct:     smPeak,
		TensorActiveAvgPct:  tensorAvg,
		TensorActivePeakPct: tensorPeak,
		DRAMActiveAvgPct:    dramAvg,
		DRAMActivePeakPct:   dramPeak,
	}
}

// aggregateGPU reduces keyed shard samples into the group roll-up + per-shard
// breakdown. The roll-up is computed over ALL readings flattened across shards
// (group mean / group peak), EXCEPT memory total which sums per-node peaks.
//
// Single-shard invariant: with exactly one shard, the roll-up reproduces the
// pre-PRD-59 GPUScraper.Stop() numbers exactly (see aggregate_test.go):
//   - util avg/peak = mean/max over that shard's samples;
//   - mem peak/avg = max/mean over that shard's samples ÷ 1024;
//   - mem total = that shard's peak (= mem peak);
//   - SM/tensor/DRAM avg/peak = mean/max, nil when absent.
//
// shardOrder gives a deterministic breakdown ordering (nodes as inserted).
func aggregateGPU(samples map[gpuShardKey]*gpuShardSamples, shardOrder []gpuShardKey) GPUAggregation {
	var agg GPUAggregation

	// Flatten every shard's series for the group mean/peak.
	var allUtil, allMem, allSM, allTensor, allDRAM []float64
	var memTotalMiB float64 // sum of per-node peak memory

	agg.Shards = make([]ShardMetrics, 0, len(shardOrder))
	for _, key := range shardOrder {
		s := samples[key]
		if s == nil {
			continue
		}
		allUtil = append(allUtil, s.util...)
		allMem = append(allMem, s.memMiB...)
		allSM = append(allSM, s.sm...)
		allTensor = append(allTensor, s.tensor...)
		allDRAM = append(allDRAM, s.dram...)

		_, peakMiB := meanPeak(s.memMiB)
		memTotalMiB += peakMiB

		agg.Shards = append(agg.Shards, reduceShard(key, s))
	}

	agg.UtilizationAvgPct, agg.UtilizationPeakPct = meanPeak(allUtil)
	memAvgMiB, memPeakMiB := meanPeak(allMem)
	agg.MemoryAvgGiB = memAvgMiB / dcgmMiBPerGiB
	agg.MemoryPeakGiB = memPeakMiB / dcgmMiBPerGiB
	agg.MemoryTotalGiB = memTotalMiB / dcgmMiBPerGiB
	agg.SMActiveAvgPct, agg.SMActivePeakPct = aggregatePctSamples(allSM)
	agg.TensorActiveAvgPct, agg.TensorActivePeakPct = aggregatePctSamples(allTensor)
	agg.DRAMActiveAvgPct, agg.DRAMActivePeakPct = aggregatePctSamples(allDRAM)

	return agg
}

// shardMetricsToDB converts the compute-layer ShardMetrics into database
// ShardMetric rows for persistence (PRD-59 Layer 3). util/mem are always-present
// scalars → non-nil pointers; the DCP metrics carry their nil (NULL) signal
// through unchanged.
func shardMetricsToDB(runID string, shards []ShardMetrics) []database.ShardMetric {
	if len(shards) == 0 {
		return nil
	}
	out := make([]database.ShardMetric, 0, len(shards))
	for _, s := range shards {
		utilAvg, utilPeak := s.UtilizationAvgPct, s.UtilizationPeakPct
		memAvg, memPeak := s.MemoryAvgGiB, s.MemoryPeakGiB
		out = append(out, database.ShardMetric{
			RunID:               runID,
			Node:                s.Node,
			Role:                s.Role,
			Samples:             s.Samples,
			UtilizationAvgPct:   &utilAvg,
			UtilizationPeakPct:  &utilPeak,
			MemoryAvgGiB:        &memAvg,
			MemoryPeakGiB:       &memPeak,
			SMActiveAvgPct:      s.SMActiveAvgPct,
			SMActivePeakPct:     s.SMActivePeakPct,
			TensorActiveAvgPct:  s.TensorActiveAvgPct,
			TensorActivePeakPct: s.TensorActivePeakPct,
			DRAMActiveAvgPct:    s.DRAMActiveAvgPct,
			DRAMActivePeakPct:   s.DRAMActivePeakPct,
		})
	}
	return out
}
