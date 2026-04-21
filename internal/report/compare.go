package report

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"html/template"
	"strings"
	"time"

	"github.com/accelbench/accelbench/internal/database"
)

var compareTemplate *template.Template

func init() {
	funcs := template.FuncMap{
		"shortRun": shortRun,
		// "map" lets us pass multiple arguments to a sub-template.
		"map": func(pairs ...interface{}) (map[string]interface{}, error) {
			if len(pairs)%2 != 0 {
				return nil, fmt.Errorf("map: odd number of args")
			}
			m := make(map[string]interface{}, len(pairs)/2)
			for i := 0; i < len(pairs); i += 2 {
				k, ok := pairs[i].(string)
				if !ok {
					return nil, fmt.Errorf("map: non-string key at index %d", i)
				}
				m[k] = pairs[i+1]
			}
			return m, nil
		},
	}
	compareTemplate = template.Must(
		template.New("compare.html").Funcs(funcs).ParseFS(templateFS, "templates/compare.html"),
	)
}

// CompareCell is one value in a metric row, optionally highlighted as a winner.
type CompareCell struct {
	Text     string
	IsWinner bool
}

// CompareRow is one metric across all entries.
type CompareRow struct {
	Label string
	Cells []CompareCell
}

// CompareReportData is the template payload.
type CompareReportData struct {
	Title            string
	Timestamp        string
	Region           string
	PricingTierLabel string
	Entries          []database.CatalogEntry
	Summary          []string
	ConfigDiff       []ConfigDiffRow
	LatencyRows      []CompareRow
	ThroughputRows   []CompareRow
	HardwareRows     []CompareRow
	MemoryRows       []CompareRow
	CostRows         []CompareRow
}

type pricingLookup func(instanceTypeName string) *float64

// GenerateCompareReport renders an HTML comparison report. `hourlyByInstance`
// maps instance type name → hourly USD rate; entries without a price get "—"
// cost cells.
func GenerateCompareReport(
	entries []database.CatalogEntry,
	hourly pricingLookup,
	tierLabel string,
	region string,
) ([]byte, error) {
	data := CompareReportData{
		Title:            fmt.Sprintf("AccelBench Compare — %d runs", len(entries)),
		Timestamp:        time.Now().Format("2006-01-02 15:04:05 MST"),
		Region:           region,
		PricingTierLabel: tierLabel,
		Entries:          entries,
		Summary:          CompareSummary(entries),
		ConfigDiff:       ConfigDiff(entries),
	}

	data.LatencyRows = buildRows(entries, []metricDef{
		{"TTFT p50 (ms)", DirectionMin, 1, func(e database.CatalogEntry) *float64 { return e.TTFTP50Ms }},
		{"TTFT p95 (ms)", DirectionMin, 1, func(e database.CatalogEntry) *float64 { return e.TTFTP95Ms }},
		{"TTFT p99 (ms)", DirectionMin, 1, func(e database.CatalogEntry) *float64 { return e.TTFTP99Ms }},
		{"E2E p50 (ms)", DirectionMin, 1, func(e database.CatalogEntry) *float64 { return e.E2ELatencyP50Ms }},
		{"E2E p95 (ms)", DirectionMin, 1, func(e database.CatalogEntry) *float64 { return e.E2ELatencyP95Ms }},
		{"E2E p99 (ms)", DirectionMin, 1, func(e database.CatalogEntry) *float64 { return e.E2ELatencyP99Ms }},
		{"ITL p50 (ms)", DirectionMin, 1, func(e database.CatalogEntry) *float64 { return e.ITLP50Ms }},
		{"ITL p95 (ms)", DirectionMin, 1, func(e database.CatalogEntry) *float64 { return e.ITLP95Ms }},
	})

	data.ThroughputRows = buildRows(entries, []metricDef{
		{"Throughput (tok/s)", DirectionMax, 0, func(e database.CatalogEntry) *float64 { return e.ThroughputAggregateTPS }},
		{"Throughput / GPU (tok/s)", DirectionMax, 1, func(e database.CatalogEntry) *float64 {
			if e.ThroughputAggregateTPS == nil || e.AcceleratorCount <= 0 {
				return nil
			}
			v := *e.ThroughputAggregateTPS / float64(e.AcceleratorCount)
			return &v
		}},
		{"Requests / sec", DirectionMax, 2, func(e database.CatalogEntry) *float64 { return e.RequestsPerSecond }},
		{"Success Rate (%)", DirectionMax, 1, successRateOf},
	})

	// Hardware utilization — no winners (direction left as "", handled below)
	data.HardwareRows = buildRowsNoWinners(entries, []metricDef{
		{"GPU Busy % (avg)", "", 0, func(e database.CatalogEntry) *float64 { return e.AcceleratorUtilizationAvgPct }},
		{"SM Active % (avg)", "", 0, func(e database.CatalogEntry) *float64 { return e.SMActiveAvgPct }},
		{"Tensor Active % (avg)", "", 0, func(e database.CatalogEntry) *float64 { return e.TensorActiveAvgPct }},
		{"DRAM Active % (avg)", "", 0, func(e database.CatalogEntry) *float64 { return e.DRAMActiveAvgPct }},
	})

	data.MemoryRows = buildRowsNoWinners(entries, []metricDef{
		{"Memory GiB (avg)", "", 1, func(e database.CatalogEntry) *float64 { return e.AcceleratorMemoryAvgGiB }},
		{"Memory GiB (peak)", "", 1, func(e database.CatalogEntry) *float64 { return e.AcceleratorMemoryPeakGiB }},
	})

	// Cost rows derived from pricing lookup.
	costMetrics := []struct {
		label    string
		get      func(e database.CatalogEntry, hr float64) *float64
		format   func(float64) string
		dir      Direction
	}{
		{
			"Hourly Cost (USD)",
			func(_ database.CatalogEntry, hr float64) *float64 { return &hr },
			func(v float64) string { return fmt.Sprintf("$%.2f", v) },
			DirectionMin,
		},
		{
			"Cost / Request (USD)",
			func(e database.CatalogEntry, hr float64) *float64 {
				if e.RequestsPerSecond == nil || *e.RequestsPerSecond <= 0 {
					return nil
				}
				v := hr / *e.RequestsPerSecond / 3600
				return &v
			},
			func(v float64) string { return fmt.Sprintf("$%.6f", v) },
			DirectionMin,
		},
		{
			"Cost / 1M Tokens (USD)",
			func(e database.CatalogEntry, hr float64) *float64 {
				if e.ThroughputAggregateTPS == nil || *e.ThroughputAggregateTPS <= 0 {
					return nil
				}
				v := (hr / *e.ThroughputAggregateTPS / 3600) * 1_000_000
				return &v
			},
			func(v float64) string { return fmt.Sprintf("$%.2f", v) },
			DirectionMin,
		},
	}
	for _, m := range costMetrics {
		vals := make([]*float64, len(entries))
		for i, e := range entries {
			hr := hourly(e.InstanceTypeName)
			if hr == nil {
				continue
			}
			vals[i] = m.get(e, *hr)
		}
		winIdx := WinnerIndex(vals, m.dir, 0.01)
		cells := make([]CompareCell, len(entries))
		for i, v := range vals {
			if v == nil {
				cells[i] = CompareCell{Text: "—"}
				continue
			}
			cells[i] = CompareCell{Text: m.format(*v), IsWinner: i == winIdx}
		}
		data.CostRows = append(data.CostRows, CompareRow{Label: m.label, Cells: cells})
	}

	var buf bytes.Buffer
	if err := compareTemplate.Execute(&buf, data); err != nil {
		return nil, fmt.Errorf("execute compare template: %w", err)
	}
	return buf.Bytes(), nil
}

type metricDef struct {
	label     string
	direction Direction
	precision int
	get       func(e database.CatalogEntry) *float64
}

func buildRows(entries []database.CatalogEntry, metrics []metricDef) []CompareRow {
	rows := make([]CompareRow, 0, len(metrics))
	for _, m := range metrics {
		vals := make([]*float64, len(entries))
		for i, e := range entries {
			vals[i] = m.get(e)
		}
		winIdx := WinnerIndex(vals, m.direction, 0.01)
		cells := make([]CompareCell, len(entries))
		for i, v := range vals {
			if v == nil {
				cells[i] = CompareCell{Text: "—"}
				continue
			}
			cells[i] = CompareCell{
				Text:     fmt.Sprintf("%.*f", m.precision, *v),
				IsWinner: i == winIdx,
			}
		}
		rows = append(rows, CompareRow{Label: m.label, Cells: cells})
	}
	return rows
}

func buildRowsNoWinners(entries []database.CatalogEntry, metrics []metricDef) []CompareRow {
	rows := make([]CompareRow, 0, len(metrics))
	for _, m := range metrics {
		cells := make([]CompareCell, len(entries))
		for i, e := range entries {
			v := m.get(e)
			if v == nil {
				cells[i] = CompareCell{Text: "—"}
				continue
			}
			cells[i] = CompareCell{Text: fmt.Sprintf("%.*f", m.precision, *v)}
		}
		rows = append(rows, CompareRow{Label: m.label, Cells: cells})
	}
	return rows
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

// shortRun returns a compact per-run label for table headers.
func shortRun(e database.CatalogEntry) string {
	return fmt.Sprintf("%s / %s", shortModel(e), e.InstanceTypeName)
}

// GenerateCompareCSV emits a one-row-per-run CSV with all Compare metrics
// plus derived values.
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

// Unused import guard for strings; reserved for future label sanitization.
var _ = strings.TrimSpace
