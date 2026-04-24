package report

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"time"

	"github.com/accelbench/accelbench/internal/database"
	"github.com/accelbench/accelbench/internal/scenario"
)

// GenerateRunCSV emits a vertical field/value CSV for a single benchmark
// run — all metadata + metrics in one file. Designed for spreadsheet
// ingestion (one property per row).
func GenerateRunCSV(
	run *database.BenchmarkRun,
	metrics *database.BenchmarkMetrics,
	details *database.RunExportDetails,
	hourlyUSD *float64,
) ([]byte, error) {
	var buf bytes.Buffer
	w := csv.NewWriter(&buf)

	w.Write([]string{"# accelbench run export"})
	w.Write([]string{fmt.Sprintf("# generated: %s", time.Now().Format(time.RFC3339))})
	w.Write([]string{fmt.Sprintf("# run_id: %s", run.ID)})
	w.Write(nil)
	w.Write([]string{"field", "value"})

	writeRow := func(field, value string) {
		w.Write([]string{field, value})
	}

	// Run metadata
	writeRow("run_id", run.ID)
	if details != nil {
		writeRow("model_hf_id", details.ModelHfID)
		writeRow("instance_type", details.InstanceTypeName)
		writeRow("accelerator_type", details.AcceleratorType)
		writeRow("accelerator_name", details.AcceleratorName)
		writeRow("accelerator_count", fmt.Sprintf("%d", details.AcceleratorCount))
	}
	writeRow("framework", run.Framework)
	writeRow("framework_version", run.FrameworkVersion)
	writeRow("tensor_parallel_degree", fmt.Sprintf("%d", run.TensorParallelDegree))
	writeRow("quantization", strOrDefault(run.Quantization, ""))
	writeRow("concurrency", fmt.Sprintf("%d", run.Concurrency))
	writeRow("input_sequence_length", fmt.Sprintf("%d", run.InputSequenceLength))
	writeRow("output_sequence_length", fmt.Sprintf("%d", run.OutputSequenceLength))
	writeRow("dataset_name", run.DatasetName)
	writeRow("status", run.Status)
	if run.StartedAt != nil {
		writeRow("started_at", run.StartedAt.Format(time.RFC3339))
	}
	if run.CompletedAt != nil {
		writeRow("completed_at", run.CompletedAt.Format(time.RFC3339))
	}

	// Metrics
	if metrics != nil {
		writeRow("ttft_p50_ms", fmtPtr(metrics.TTFTP50Ms, 2))
		writeRow("ttft_p90_ms", fmtPtr(metrics.TTFTP90Ms, 2))
		writeRow("ttft_p95_ms", fmtPtr(metrics.TTFTP95Ms, 2))
		writeRow("ttft_p99_ms", fmtPtr(metrics.TTFTP99Ms, 2))
		writeRow("e2e_latency_p50_ms", fmtPtr(metrics.E2ELatencyP50Ms, 2))
		writeRow("e2e_latency_p90_ms", fmtPtr(metrics.E2ELatencyP90Ms, 2))
		writeRow("e2e_latency_p95_ms", fmtPtr(metrics.E2ELatencyP95Ms, 2))
		writeRow("e2e_latency_p99_ms", fmtPtr(metrics.E2ELatencyP99Ms, 2))
		writeRow("itl_p50_ms", fmtPtr(metrics.ITLP50Ms, 2))
		writeRow("itl_p90_ms", fmtPtr(metrics.ITLP90Ms, 2))
		writeRow("itl_p95_ms", fmtPtr(metrics.ITLP95Ms, 2))
		writeRow("itl_p99_ms", fmtPtr(metrics.ITLP99Ms, 2))
		writeRow("tpot_p50_ms", fmtPtr(metrics.TPOTP50Ms, 2))
		writeRow("tpot_p90_ms", fmtPtr(metrics.TPOTP90Ms, 2))
		writeRow("tpot_p99_ms", fmtPtr(metrics.TPOTP99Ms, 2))
		writeRow("throughput_per_request_tps", fmtPtr(metrics.ThroughputPerRequestTPS, 2))
		writeRow("throughput_aggregate_tps", fmtPtr(metrics.ThroughputAggregateTPS, 2))
		writeRow("requests_per_second", fmtPtr(metrics.RequestsPerSecond, 3))
		writeRow("successful_requests", fmtIntPtr(metrics.SuccessfulRequests))
		writeRow("failed_requests", fmtIntPtr(metrics.FailedRequests))
		writeRow("total_duration_seconds", fmtPtr(metrics.TotalDurationSeconds, 1))
		writeRow("accelerator_utilization_pct", fmtPtr(metrics.AcceleratorUtilizationPct, 1))
		writeRow("accelerator_utilization_avg_pct", fmtPtr(metrics.AcceleratorUtilizationAvgPct, 1))
		writeRow("accelerator_memory_peak_gib", fmtPtr(metrics.AcceleratorMemoryPeakGiB, 2))
		writeRow("accelerator_memory_avg_gib", fmtPtr(metrics.AcceleratorMemoryAvgGiB, 2))
		writeRow("sm_active_avg_pct", fmtPtr(metrics.SMActiveAvgPct, 1))
		writeRow("sm_active_peak_pct", fmtPtr(metrics.SMActivePeakPct, 1))
		writeRow("tensor_active_avg_pct", fmtPtr(metrics.TensorActiveAvgPct, 1))
		writeRow("tensor_active_peak_pct", fmtPtr(metrics.TensorActivePeakPct, 1))
		writeRow("dram_active_avg_pct", fmtPtr(metrics.DRAMActiveAvgPct, 1))
		writeRow("dram_active_peak_pct", fmtPtr(metrics.DRAMActivePeakPct, 1))
		writeRow("kv_cache_utilization_avg_pct", fmtPtr(metrics.KVCacheUtilizationAvgPct, 1))
		writeRow("kv_cache_utilization_peak_pct", fmtPtr(metrics.KVCacheUtilizationPeakPct, 1))
		writeRow("prefix_cache_hit_rate", fmtPtr(metrics.PrefixCacheHitRate, 3))
	}

	// Cost
	writeRow("hourly_usd", fmtPtr(hourlyUSD, 4))
	writeRow("total_cost_usd", fmtPtr(run.TotalCostUSD, 4))
	writeRow("loadgen_cost_usd", fmtPtr(run.LoadgenCostUSD, 4))

	w.Flush()
	if err := w.Error(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// GenerateSuiteCSV emits a one-row-per-scenario CSV for a test suite
// run — designed to make the scaling curves trivially plottable in
// Excel or pandas.
func GenerateSuiteCSV(
	suite *database.TestSuiteRun,
	results []database.ScenarioResult,
	model *database.Model,
	instance *database.InstanceType,
) ([]byte, error) {
	var buf bytes.Buffer
	w := csv.NewWriter(&buf)

	w.Write([]string{"# accelbench suite export"})
	w.Write([]string{fmt.Sprintf("# generated: %s", time.Now().Format(time.RFC3339))})
	w.Write([]string{fmt.Sprintf("# suite_run_id: %s", suite.ID)})
	if model != nil {
		w.Write([]string{fmt.Sprintf("# model: %s", model.HfID)})
	}
	if instance != nil {
		w.Write([]string{fmt.Sprintf("# instance: %s", instance.Name)})
	}
	w.Write(nil)

	headers := []string{
		"scenario_id", "scenario_name", "target_qps", "duration_seconds", "status",
		"ttft_p50_ms", "ttft_p90_ms", "ttft_p99_ms",
		"e2e_latency_p50_ms", "e2e_latency_p90_ms", "e2e_latency_p99_ms",
		"itl_p50_ms", "itl_p90_ms", "itl_p99_ms",
		"tpot_p50_ms", "tpot_p90_ms", "tpot_p99_ms",
		"throughput_tps", "requests_per_second",
		"successful_requests", "failed_requests",
		"accelerator_utilization_avg_pct", "accelerator_memory_avg_gib", "accelerator_memory_peak_gib",
		"sm_active_avg_pct", "tensor_active_avg_pct", "dram_active_avg_pct",
	}
	w.Write(headers)

	for _, r := range results {
		scName := r.ScenarioID
		targetQPS := ""
		duration := ""
		if sc := scenario.Get(r.ScenarioID); sc != nil {
			scName = sc.Name
			targetQPS = fmt.Sprintf("%d", sc.TargetQPS())
			duration = fmt.Sprintf("%d", sc.TotalDuration())
		}

		row := []string{
			r.ScenarioID,
			scName,
			targetQPS,
			duration,
			r.Status,
			fmtPtr(r.TTFTP50Ms, 2),
			fmtPtr(r.TTFTP90Ms, 2),
			fmtPtr(r.TTFTP99Ms, 2),
			fmtPtr(r.E2ELatencyP50Ms, 2),
			fmtPtr(r.E2ELatencyP90Ms, 2),
			fmtPtr(r.E2ELatencyP99Ms, 2),
			fmtPtr(r.ITLP50Ms, 2),
			fmtPtr(r.ITLP90Ms, 2),
			fmtPtr(r.ITLP99Ms, 2),
			fmtPtr(r.TPOTP50Ms, 2),
			fmtPtr(r.TPOTP90Ms, 2),
			fmtPtr(r.TPOTP99Ms, 2),
			fmtPtr(r.ThroughputTPS, 2),
			fmtPtr(r.RequestsPerSecond, 3),
			fmtIntPtr(r.SuccessfulRequests),
			fmtIntPtr(r.FailedRequests),
			fmtPtr(r.AcceleratorUtilizationAvgPct, 1),
			fmtPtr(r.AcceleratorMemoryAvgGiB, 2),
			fmtPtr(r.AcceleratorMemoryPeakGiB, 2),
			fmtPtr(r.SMActiveAvgPct, 1),
			fmtPtr(r.TensorActiveAvgPct, 1),
			fmtPtr(r.DRAMActiveAvgPct, 1),
		}
		w.Write(row)
	}

	w.Flush()
	if err := w.Error(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
