package report

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"time"

	"github.com/accelbench/accelbench/internal/database"
)

// pricingLookup maps an instance type name → hourly USD rate for the
// currently selected pricing tier. Returns nil when no pricing is known.
type pricingLookup func(instanceTypeName string) *float64

// GenerateCompareCSV emits a one-row-per-run CSV with all Compare metrics
// plus derived values (per-GPU throughput, cost per request, cost per 1M tokens).
func GenerateCompareCSV(
	entries []database.CatalogEntry,
	hourly pricingLookup,
	tierLabel string,
	region string,
) ([]byte, error) {
	var buf bytes.Buffer
	w := csv.NewWriter(&buf)

	w.Write([]string{"# accelbench compare export"})
	w.Write([]string{fmt.Sprintf("# generated: %s", time.Now().Format(time.RFC3339))})
	w.Write([]string{fmt.Sprintf("# region: %s, pricing: %s", region, tierLabel)})
	w.Write(nil)

	headers := []string{
		"run_id", "model_hf_id", "instance_type", "accelerator_count", "framework", "framework_version",
		"tp_degree", "quantization", "concurrency", "input_seq", "output_seq",
		"ttft_p50_ms", "ttft_p95_ms", "ttft_p99_ms",
		"e2e_p50_ms", "e2e_p95_ms", "e2e_p99_ms",
		"itl_p50_ms", "itl_p95_ms", "itl_p99_ms",
		"throughput_tps", "throughput_per_gpu_tps",
		"rps", "successful", "failed", "success_rate_pct",
		"gpu_busy_avg_pct", "sm_active_avg_pct", "tensor_active_avg_pct", "dram_active_avg_pct",
		"memory_avg_gib", "memory_peak_gib",
		"hourly_usd", "cost_per_request_usd", "cost_per_1m_tokens_usd",
	}
	w.Write(headers)

	for _, e := range entries {
		hr := hourly(e.InstanceTypeName)
		row := []string{
			e.RunID,
			e.ModelHfID,
			e.InstanceTypeName,
			fmt.Sprintf("%d", e.AcceleratorCount),
			e.Framework,
			e.FrameworkVersion,
			fmt.Sprintf("%d", e.TensorParallelDegree),
			strOrDefault(e.Quantization, "default"),
			fmt.Sprintf("%d", e.Concurrency),
			fmt.Sprintf("%d", e.InputSequenceLength),
			fmt.Sprintf("%d", e.OutputSequenceLength),
			fmtPtr(e.TTFTP50Ms, 2),
			fmtPtr(e.TTFTP95Ms, 2),
			fmtPtr(e.TTFTP99Ms, 2),
			fmtPtr(e.E2ELatencyP50Ms, 2),
			fmtPtr(e.E2ELatencyP95Ms, 2),
			fmtPtr(e.E2ELatencyP99Ms, 2),
			fmtPtr(e.ITLP50Ms, 2),
			fmtPtr(e.ITLP95Ms, 2),
			fmtPtr(e.ITLP99Ms, 2),
			fmtPtr(e.ThroughputAggregateTPS, 2),
			fmtPtr(perGPU(e), 2),
			fmtPtr(e.RequestsPerSecond, 3),
			fmtIntPtr(e.SuccessfulRequests),
			fmtIntPtr(e.FailedRequests),
			fmtPtr(successRateOf(e), 2),
			fmtPtr(e.AcceleratorUtilizationAvgPct, 1),
			fmtPtr(e.SMActiveAvgPct, 1),
			fmtPtr(e.TensorActiveAvgPct, 1),
			fmtPtr(e.DRAMActiveAvgPct, 1),
			fmtPtr(e.AcceleratorMemoryAvgGiB, 2),
			fmtPtr(e.AcceleratorMemoryPeakGiB, 2),
			fmtPtr(hr, 4),
			fmtPtr(costPerRequest(e, hr), 6),
			fmtPtr(costPer1M(e, hr), 4),
		}
		w.Write(row)
	}
	w.Flush()
	if err := w.Error(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func perGPU(e database.CatalogEntry) *float64 {
	if e.ThroughputAggregateTPS == nil || e.AcceleratorCount <= 0 {
		return nil
	}
	v := *e.ThroughputAggregateTPS / float64(e.AcceleratorCount)
	return &v
}

func costPerRequest(e database.CatalogEntry, hr *float64) *float64 {
	if hr == nil || e.RequestsPerSecond == nil || *e.RequestsPerSecond <= 0 {
		return nil
	}
	v := *hr / *e.RequestsPerSecond / 3600
	return &v
}

func costPer1M(e database.CatalogEntry, hr *float64) *float64 {
	if hr == nil || e.ThroughputAggregateTPS == nil || *e.ThroughputAggregateTPS <= 0 {
		return nil
	}
	v := (*hr / *e.ThroughputAggregateTPS / 3600) * 1_000_000
	return &v
}

func successRateOf(e database.CatalogEntry) *float64 {
	ok, failed := 0, 0
	if e.SuccessfulRequests != nil {
		ok = *e.SuccessfulRequests
	}
	if e.FailedRequests != nil {
		failed = *e.FailedRequests
	}
	total := ok + failed
	if total == 0 {
		return nil
	}
	v := float64(ok) / float64(total) * 100
	return &v
}

func fmtPtr(v *float64, precision int) string {
	if v == nil {
		return ""
	}
	return fmt.Sprintf("%.*f", precision, *v)
}

func fmtIntPtr(v *int) string {
	if v == nil {
		return ""
	}
	return fmt.Sprintf("%d", *v)
}

func strOrDefault(p *string, def string) string {
	if p == nil || *p == "" {
		return def
	}
	return *p
}
