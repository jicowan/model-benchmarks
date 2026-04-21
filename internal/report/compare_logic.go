package report

import (
	"fmt"
	"math"

	"github.com/accelbench/accelbench/internal/database"
)

// Direction indicates whether "winning" means the smallest or largest value.
type Direction string

const (
	DirectionMin Direction = "min"
	DirectionMax Direction = "max"
)

// WinnerIndex returns the index of the winning value in `values`, or -1 if
// fewer than two finite values are present or the leader is tied with another
// within `tolerance` (relative). Mirrors the frontend's lib/compare.ts so
// exports highlight the same winners as the UI.
func WinnerIndex(values []*float64, dir Direction, tolerance float64) int {
	type present struct {
		idx int
		v   float64
	}
	var p []present
	for i, v := range values {
		if v != nil && !math.IsNaN(*v) && !math.IsInf(*v, 0) {
			p = append(p, present{i, *v})
		}
	}
	if len(p) < 2 {
		return -1
	}
	best := p[0]
	for _, x := range p[1:] {
		if dir == DirectionMin && x.v < best.v {
			best = x
		} else if dir == DirectionMax && x.v > best.v {
			best = x
		}
	}
	for _, x := range p {
		if x.idx == best.idx {
			continue
		}
		diff := math.Abs(x.v - best.v)
		rel := 0.0
		if best.v != 0 {
			rel = diff / math.Abs(best.v)
		}
		if rel <= tolerance {
			return -1
		}
	}
	return best.idx
}

// ConfigDiffRow captures one differing config field for a set of runs.
type ConfigDiffRow struct {
	Label  string
	Values []string
}

// ConfigDiff returns the config fields that differ across the entries. Fields
// where every entry agrees are omitted.
func ConfigDiff(entries []database.CatalogEntry) []ConfigDiffRow {
	if len(entries) < 2 {
		return nil
	}
	fields := []struct {
		label string
		get   func(e database.CatalogEntry) string
	}{
		{"Model", func(e database.CatalogEntry) string { return e.ModelHfID }},
		{"Instance", func(e database.CatalogEntry) string { return e.InstanceTypeName }},
		{"TP Degree", func(e database.CatalogEntry) string { return fmt.Sprintf("%d", e.TensorParallelDegree) }},
		{"Quantization", func(e database.CatalogEntry) string {
			if e.Quantization == nil || *e.Quantization == "" {
				return "default"
			}
			return *e.Quantization
		}},
		{"Concurrency", func(e database.CatalogEntry) string { return fmt.Sprintf("%d", e.Concurrency) }},
		{"Framework", func(e database.CatalogEntry) string {
			return fmt.Sprintf("%s %s", e.Framework, e.FrameworkVersion)
		}},
		{"Input Seq", func(e database.CatalogEntry) string { return fmt.Sprintf("%d", e.InputSequenceLength) }},
		{"Output Seq", func(e database.CatalogEntry) string { return fmt.Sprintf("%d", e.OutputSequenceLength) }},
	}
	var rows []ConfigDiffRow
	for _, f := range fields {
		vals := make([]string, len(entries))
		for i, e := range entries {
			vals[i] = f.get(e)
		}
		same := true
		for _, v := range vals[1:] {
			if v != vals[0] {
				same = false
				break
			}
		}
		if !same {
			rows = append(rows, ConfigDiffRow{Label: f.label, Values: vals})
		}
	}
	return rows
}

// CompareSummary produces a short list of auto-derived sentences highlighting
// the most notable winners across the entries. Caps at 5 sentences.
func CompareSummary(entries []database.CatalogEntry) []string {
	if len(entries) < 2 {
		return nil
	}
	labels := make([]string, len(entries))
	for i, e := range entries {
		labels[i] = shortModel(e)
	}
	var sentences []string

	try := func(metric, unit string, dir Direction, values []*float64, format func(float64) string) {
		idx := WinnerIndex(values, dir, 0.01)
		if idx < 0 {
			return
		}
		best := *values[idx]
		runner := -1
		for i, v := range values {
			if i == idx || v == nil {
				continue
			}
			if runner < 0 {
				runner = i
				continue
			}
			rv := *values[runner]
			if (dir == DirectionMin && *v < rv) || (dir == DirectionMax && *v > rv) {
				runner = i
			}
		}
		if runner < 0 {
			return
		}
		runnerV := *values[runner]
		var deltaPct float64
		if runnerV != 0 {
			deltaPct = (best - runnerV) / runnerV * 100
		}
		sign := "+"
		if dir == DirectionMin {
			sign = "-"
		}
		sentences = append(sentences,
			fmt.Sprintf("%s leads on %s (%s %s vs %s %s, %s%.0f%%).",
				labels[idx], metric,
				format(best), unit, format(runnerV), unit,
				sign, math.Abs(deltaPct),
			))
	}

	try("TTFT p99", "ms", DirectionMin,
		collect(entries, func(e database.CatalogEntry) *float64 { return e.TTFTP99Ms }),
		func(v float64) string { return fmt.Sprintf("%.0f", v) },
	)
	try("Throughput", "tok/s", DirectionMax,
		collect(entries, func(e database.CatalogEntry) *float64 { return e.ThroughputAggregateTPS }),
		func(v float64) string { return fmt.Sprintf("%.0f", v) },
	)

	// Success rate — either a winner sentence or a floor note.
	rates := make([]*float64, len(entries))
	allAtLeast99 := true
	for i, e := range entries {
		ok, failed := 0, 0
		if e.SuccessfulRequests != nil {
			ok = *e.SuccessfulRequests
		}
		if e.FailedRequests != nil {
			failed = *e.FailedRequests
		}
		if total := ok + failed; total > 0 {
			pct := float64(ok) / float64(total) * 100
			rates[i] = &pct
			if pct < 99 {
				allAtLeast99 = false
			}
		} else {
			allAtLeast99 = false
		}
	}
	if allAtLeast99 {
		sentences = append(sentences, "All runs exceed 99% success rate.")
	} else {
		try("Success Rate", "%", DirectionMax, rates,
			func(v float64) string { return fmt.Sprintf("%.1f", v) },
		)
	}

	if len(sentences) > 5 {
		sentences = sentences[:5]
	}
	return sentences
}

func collect(entries []database.CatalogEntry, get func(e database.CatalogEntry) *float64) []*float64 {
	out := make([]*float64, len(entries))
	for i, e := range entries {
		out[i] = get(e)
	}
	return out
}

// shortModel returns the last path component of a HF model id (or the S3 URI
// basename) — used as a compact label when pointing at a specific run.
func shortModel(e database.CatalogEntry) string {
	m := e.ModelHfID
	for i := len(m) - 1; i >= 0; i-- {
		if m[i] == '/' {
			return m[i+1:]
		}
	}
	if m != "" {
		return m
	}
	return e.RunID[:8]
}
