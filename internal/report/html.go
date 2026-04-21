package report

import (
	"bytes"
	"embed"
	"fmt"
	"html/template"
	"strings"
	"time"

	"github.com/accelbench/accelbench/internal/database"
)

//go:embed templates/*.html
var templateFS embed.FS

var runTemplate *template.Template

func init() {
	funcMap := template.FuncMap{
		"formatFloat": formatFloat,
		"formatInt":   formatInt,
		"formatUSD":   formatUSD,
		"deref":       deref,
		"derefInt":    derefInt,
		"latencySVG":  latencyDistributionSVG,
	}

	runTemplate = template.Must(template.New("run.html").Funcs(funcMap).ParseFS(templateFS, "templates/run.html"))
}

// ReportData holds all data needed to render a single run report.
type ReportData struct {
	Title        string
	ModelHfID    string
	InstanceType string
	AcceleratorSummary string // "L40S · 1 × 46 GiB"
	Timestamp    string
	Config       ConfigData
	Metrics      *database.BenchmarkMetrics

	// Hero
	AcceleratorLabel string // "GPU" or "chip" depending on accelerator_type

	// Derived display values
	SuccessRatePct        *float64
	ThroughputPerAccelTPS *float64
	HourlyRateUSD         *float64
	CostPerRequestUSD     *float64
	CostPer1MTokensUSD    *float64
	TotalSpentUSD         *float64

	// Latency distribution per metric — for the inline-SVG small multiples.
	LatencyTTFT LatencyDistribution
	LatencyE2E  LatencyDistribution
	LatencyITL  LatencyDistribution
	LatencyTPOT LatencyDistribution
}

// LatencyDistribution carries p50/p90/p95/p99 values for one latency metric.
type LatencyDistribution struct {
	Label string
	P50   *float64
	P90   *float64
	P95   *float64
	P99   *float64
}

// ConfigData holds configuration details for the report.
type ConfigData struct {
	Framework            string
	FrameworkVersion     string
	TensorParallel       int
	Quantization         string
	MaxModelLen          int
	Concurrency          int
	InputSequenceLength  int
	OutputSequenceLength int
	DatasetName          string
	RunType              string
}

// GenerateRunReport generates a self-contained HTML report for a single benchmark run.
// hourlyRateUSD may be nil when pricing is unavailable; the Cost section renders "—".
func GenerateRunReport(
	run *database.BenchmarkRun,
	metrics *database.BenchmarkMetrics,
	details *database.RunExportDetails,
	hourlyRateUSD *float64,
) ([]byte, error) {
	quant := "None"
	if run.Quantization != nil && *run.Quantization != "" {
		quant = *run.Quantization
	}

	acceleratorLabel := "GPU"
	if strings.EqualFold(details.AcceleratorType, "neuron") {
		acceleratorLabel = "chip"
	}

	data := ReportData{
		Title:        fmt.Sprintf("AccelBench Report — %s", details.ModelHfID),
		ModelHfID:    details.ModelHfID,
		InstanceType: details.InstanceTypeName,
		AcceleratorSummary: fmt.Sprintf(
			"%s · %d×%s · %d GiB",
			details.InstanceTypeName,
			details.AcceleratorCount,
			accelShortName(details),
			details.AcceleratorMemoryGiB,
		),
		Timestamp: time.Now().Format("2006-01-02 15:04:05 MST"),
		Config: ConfigData{
			Framework:            run.Framework,
			FrameworkVersion:     run.FrameworkVersion,
			TensorParallel:       run.TensorParallelDegree,
			Quantization:         quant,
			MaxModelLen:          details.MaxModelLen,
			Concurrency:          run.Concurrency,
			InputSequenceLength:  run.InputSequenceLength,
			OutputSequenceLength: run.OutputSequenceLength,
			DatasetName:          run.DatasetName,
			RunType:              run.RunType,
		},
		Metrics:          metrics,
		AcceleratorLabel: acceleratorLabel,
		LatencyTTFT: LatencyDistribution{
			Label: "TTFT",
			P50:   metrics.TTFTP50Ms,
			P90:   metrics.TTFTP90Ms,
			P95:   metrics.TTFTP95Ms,
			P99:   metrics.TTFTP99Ms,
		},
		LatencyE2E: LatencyDistribution{
			Label: "E2E",
			P50:   metrics.E2ELatencyP50Ms,
			P90:   metrics.E2ELatencyP90Ms,
			P95:   metrics.E2ELatencyP95Ms,
			P99:   metrics.E2ELatencyP99Ms,
		},
		LatencyITL: LatencyDistribution{
			Label: "ITL",
			P50:   metrics.ITLP50Ms,
			P90:   metrics.ITLP90Ms,
			P95:   metrics.ITLP95Ms,
			P99:   metrics.ITLP99Ms,
		},
		LatencyTPOT: LatencyDistribution{
			Label: "TPOT",
			P50:   metrics.TPOTP50Ms,
			P90:   metrics.TPOTP90Ms,
			P99:   metrics.TPOTP99Ms,
		},
	}

	// Success Rate %
	if metrics.SuccessfulRequests != nil {
		ok := *metrics.SuccessfulRequests
		failed := 0
		if metrics.FailedRequests != nil {
			failed = *metrics.FailedRequests
		}
		total := ok + failed
		if total > 0 {
			pct := float64(ok) / float64(total) * 100
			data.SuccessRatePct = &pct
		}
	}

	// Throughput per accelerator
	var aggregate *float64
	if metrics.ThroughputAggregateTPS != nil {
		aggregate = metrics.ThroughputAggregateTPS
	} else if metrics.GenerationThroughputTPS != nil {
		aggregate = metrics.GenerationThroughputTPS
	}
	if details.AcceleratorCount > 0 && aggregate != nil {
		per := *aggregate / float64(details.AcceleratorCount)
		data.ThroughputPerAccelTPS = &per
	}

	// Cost derivations
	if hourlyRateUSD != nil {
		data.HourlyRateUSD = hourlyRateUSD
		if metrics.RequestsPerSecond != nil && *metrics.RequestsPerSecond > 0 {
			v := *hourlyRateUSD / *metrics.RequestsPerSecond / 3600
			data.CostPerRequestUSD = &v
		}
		if aggregate != nil && *aggregate > 0 {
			v := (*hourlyRateUSD / *aggregate / 3600) * 1_000_000
			data.CostPer1MTokensUSD = &v
		}
		if metrics.TotalDurationSeconds != nil && *metrics.TotalDurationSeconds > 0 {
			v := *hourlyRateUSD * (*metrics.TotalDurationSeconds / 3600)
			data.TotalSpentUSD = &v
		}
	}

	var buf bytes.Buffer
	if err := runTemplate.Execute(&buf, data); err != nil {
		return nil, fmt.Errorf("execute template: %w", err)
	}

	return buf.Bytes(), nil
}

func accelShortName(details *database.RunExportDetails) string {
	if details.AcceleratorName != "" {
		return details.AcceleratorName
	}
	return strings.ToUpper(details.AcceleratorType)
}

// Template helper functions

func formatFloat(v *float64) string {
	if v == nil {
		return "—"
	}
	return fmt.Sprintf("%.1f", *v)
}

func formatInt(v *int) string {
	if v == nil {
		return "—"
	}
	return fmt.Sprintf("%d", *v)
}

func formatUSD(v *float64, precision int) string {
	if v == nil {
		return "—"
	}
	return fmt.Sprintf("$%.*f", precision, *v)
}

func deref(v *float64) float64 {
	if v == nil {
		return 0
	}
	return *v
}

func derefInt(v *int) int {
	if v == nil {
		return 0
	}
	return *v
}

// latencyDistributionSVG renders a single latency metric (p50/p90/p95/p99) as
// inline SVG using the same visual language as the UI's LatencyDistribution
// component: horizontal bars with percentile labels on the left and numeric
// readout on the right. Fading opacity from p50 → p99.
func latencyDistributionSVG(d LatencyDistribution) template.HTML {
	type row struct {
		key     string
		value   *float64
		opacity float64
	}
	rows := []row{
		{"p50", d.P50, 1.0},
		{"p90", d.P90, 0.72},
		{"p95", d.P95, 0.5},
		{"p99", d.P99, 0.3},
	}

	var scale float64
	for _, r := range rows {
		if r.value != nil && *r.value > scale {
			scale = *r.value
		}
	}

	const (
		widthPx     = 280
		rowHeight   = 20
		rowGap      = 6
		labelWidth  = 36
		valueWidth  = 52
		barHeight   = 14
	)
	barMaxWidth := float64(widthPx - labelWidth - valueWidth - 12)
	totalHeight := len(rows)*(rowHeight+rowGap) - rowGap

	var sb strings.Builder
	fmt.Fprintf(&sb,
		`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %d %d" width="100%%" height="%d" style="display:block">`,
		widthPx, totalHeight, totalHeight,
	)
	if scale == 0 {
		fmt.Fprintf(&sb, `<text x="4" y="14" class="rpt-mono" fill="rgb(var(--ink-2))">—</text>`)
	} else {
		for i, r := range rows {
			y := i * (rowHeight + rowGap)
			fmt.Fprintf(&sb,
				`<text x="0" y="%d" class="rpt-eyebrow" fill="rgb(var(--ink-2))">%s</text>`,
				y+11, r.key,
			)
			fmt.Fprintf(&sb,
				`<rect x="%d" y="%d" width="%.2f" height="%d" fill="rgb(var(--surface-2))"/>`,
				labelWidth, y+(rowHeight-barHeight)/2, barMaxWidth, barHeight,
			)
			if r.value != nil {
				barW := (*r.value / scale) * barMaxWidth
				fmt.Fprintf(&sb,
					`<rect x="%d" y="%d" width="%.2f" height="%d" fill="rgb(var(--signal))" opacity="%.2f"/>`,
					labelWidth, y+(rowHeight-barHeight)/2, barW, barHeight, r.opacity,
				)
				fmt.Fprintf(&sb,
					`<text x="%d" y="%d" text-anchor="end" class="rpt-mono" fill="rgb(var(--ink-0))">%.0f</text>`,
					widthPx-4, y+14, *r.value,
				)
			} else {
				fmt.Fprintf(&sb,
					`<text x="%d" y="%d" text-anchor="end" class="rpt-mono" fill="rgb(var(--ink-2))">—</text>`,
					widthPx-4, y+14,
				)
			}
		}
	}
	sb.WriteString(`</svg>`)
	return template.HTML(sb.String())
}
