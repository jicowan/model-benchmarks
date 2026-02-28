package database

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// CatalogEntry is a denormalized view joining benchmark runs, models,
// instance types, and metrics for catalog display.
type CatalogEntry struct {
	RunID                string   `json:"run_id"`
	ModelHfID            string   `json:"model_hf_id"`
	ModelFamily          *string  `json:"model_family,omitempty"`
	ParameterCount       *int64   `json:"parameter_count,omitempty"`
	InstanceTypeName     string   `json:"instance_type_name"`
	InstanceFamily       string   `json:"instance_family"`
	AcceleratorType      string   `json:"accelerator_type"`
	AcceleratorName      string   `json:"accelerator_name"`
	AcceleratorCount     int      `json:"accelerator_count"`
	AcceleratorMemoryGiB int      `json:"accelerator_memory_gib"`
	Framework            string   `json:"framework"`
	FrameworkVersion     string   `json:"framework_version"`
	TensorParallelDegree int      `json:"tensor_parallel_degree"`
	Quantization         *string  `json:"quantization,omitempty"`
	Concurrency          int      `json:"concurrency"`
	InputSequenceLength  int      `json:"input_sequence_length"`
	OutputSequenceLength int      `json:"output_sequence_length"`
	CompletedAt          *time.Time `json:"completed_at,omitempty"`

	// Metrics (inlined from benchmark_metrics)
	TTFTP50Ms                *float64 `json:"ttft_p50_ms,omitempty"`
	TTFTP99Ms                *float64 `json:"ttft_p99_ms,omitempty"`
	E2ELatencyP50Ms          *float64 `json:"e2e_latency_p50_ms,omitempty"`
	E2ELatencyP99Ms          *float64 `json:"e2e_latency_p99_ms,omitempty"`
	ITLP50Ms                 *float64 `json:"itl_p50_ms,omitempty"`
	ITLP99Ms                 *float64 `json:"itl_p99_ms,omitempty"`
	ThroughputPerRequestTPS  *float64 `json:"throughput_per_request_tps,omitempty"`
	ThroughputAggregateTPS   *float64 `json:"throughput_aggregate_tps,omitempty"`
	RequestsPerSecond        *float64 `json:"requests_per_second,omitempty"`
	AcceleratorUtilizationPct *float64 `json:"accelerator_utilization_pct,omitempty"`
	AcceleratorMemoryPeakGiB *float64 `json:"accelerator_memory_peak_gib,omitempty"`
}

// CatalogFilter holds optional filters for catalog queries.
type CatalogFilter struct {
	ModelHfID       string // exact match on model hf_id
	ModelFamily     string // exact match on model_family
	InstanceFamily  string // exact match on instance family (e.g. "p5")
	AcceleratorType string // "gpu" or "neuron"
	SortBy          string // column name to sort by
	SortDesc        bool   // true for descending sort
	Limit           int    // max results (0 = default 100)
	Offset          int    // pagination offset
}

// allowedSortColumns maps user-facing sort keys to SQL column expressions.
var allowedSortColumns = map[string]string{
	"model":                    "m.hf_id",
	"instance":                 "it.name",
	"ttft_p50":                 "bm.ttft_p50_ms",
	"ttft_p99":                 "bm.ttft_p99_ms",
	"e2e_latency_p50":          "bm.e2e_latency_p50_ms",
	"e2e_latency_p99":          "bm.e2e_latency_p99_ms",
	"itl_p50":                  "bm.itl_p50_ms",
	"itl_p99":                  "bm.itl_p99_ms",
	"throughput_per_request":    "bm.throughput_per_request_tps",
	"throughput_aggregate":      "bm.throughput_aggregate_tps",
	"requests_per_second":       "bm.requests_per_second",
	"accelerator_utilization":   "bm.accelerator_utilization_pct",
	"accelerator_memory_peak":   "bm.accelerator_memory_peak_gib",
	"completed_at":             "br.completed_at",
}

// ListCatalog queries the catalog with optional filters and sorting.
func (r *Repository) ListCatalog(ctx context.Context, f CatalogFilter) ([]CatalogEntry, error) {
	var (
		conditions []string
		args       []any
		argIdx     int
	)

	// Always filter to completed, non-superseded runs.
	conditions = append(conditions, "br.status = 'completed'")
	conditions = append(conditions, "br.superseded = FALSE")

	if f.ModelHfID != "" {
		argIdx++
		conditions = append(conditions, fmt.Sprintf("m.hf_id = $%d", argIdx))
		args = append(args, f.ModelHfID)
	}
	if f.ModelFamily != "" {
		argIdx++
		conditions = append(conditions, fmt.Sprintf("m.model_family = $%d", argIdx))
		args = append(args, f.ModelFamily)
	}
	if f.InstanceFamily != "" {
		argIdx++
		conditions = append(conditions, fmt.Sprintf("it.family = $%d", argIdx))
		args = append(args, f.InstanceFamily)
	}
	if f.AcceleratorType != "" {
		argIdx++
		conditions = append(conditions, fmt.Sprintf("it.accelerator_type = $%d", argIdx))
		args = append(args, f.AcceleratorType)
	}

	where := "WHERE " + strings.Join(conditions, " AND ")

	// Sort.
	orderBy := "ORDER BY m.hf_id, it.name"
	if f.SortBy != "" {
		if col, ok := allowedSortColumns[f.SortBy]; ok {
			dir := "ASC"
			if f.SortDesc {
				dir = "DESC"
			}
			orderBy = fmt.Sprintf("ORDER BY %s %s NULLS LAST", col, dir)
		}
	}

	// Pagination.
	limit := 100
	if f.Limit > 0 && f.Limit <= 500 {
		limit = f.Limit
	}
	argIdx++
	limitClause := fmt.Sprintf("LIMIT $%d", argIdx)
	args = append(args, limit)

	offsetClause := ""
	if f.Offset > 0 {
		argIdx++
		offsetClause = fmt.Sprintf("OFFSET $%d", argIdx)
		args = append(args, f.Offset)
	}

	query := fmt.Sprintf(`
		SELECT
			br.id, m.hf_id, m.model_family, m.parameter_count,
			it.name, it.family, it.accelerator_type, it.accelerator_name,
			it.accelerator_count, it.accelerator_memory_gib,
			br.framework, br.framework_version, br.tensor_parallel_degree,
			br.quantization, br.concurrency,
			br.input_sequence_length, br.output_sequence_length,
			br.completed_at,
			bm.ttft_p50_ms, bm.ttft_p99_ms,
			bm.e2e_latency_p50_ms, bm.e2e_latency_p99_ms,
			bm.itl_p50_ms, bm.itl_p99_ms,
			bm.throughput_per_request_tps, bm.throughput_aggregate_tps,
			bm.requests_per_second,
			bm.accelerator_utilization_pct, bm.accelerator_memory_peak_gib
		FROM benchmark_runs br
		JOIN models m ON br.model_id = m.id
		JOIN instance_types it ON br.instance_type_id = it.id
		JOIN benchmark_metrics bm ON bm.run_id = br.id
		%s
		%s
		%s %s
	`, where, orderBy, limitClause, offsetClause)

	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query catalog: %w", err)
	}
	defer rows.Close()

	var entries []CatalogEntry
	for rows.Next() {
		var e CatalogEntry
		err := rows.Scan(
			&e.RunID, &e.ModelHfID, &e.ModelFamily, &e.ParameterCount,
			&e.InstanceTypeName, &e.InstanceFamily, &e.AcceleratorType, &e.AcceleratorName,
			&e.AcceleratorCount, &e.AcceleratorMemoryGiB,
			&e.Framework, &e.FrameworkVersion, &e.TensorParallelDegree,
			&e.Quantization, &e.Concurrency,
			&e.InputSequenceLength, &e.OutputSequenceLength,
			&e.CompletedAt,
			&e.TTFTP50Ms, &e.TTFTP99Ms,
			&e.E2ELatencyP50Ms, &e.E2ELatencyP99Ms,
			&e.ITLP50Ms, &e.ITLP99Ms,
			&e.ThroughputPerRequestTPS, &e.ThroughputAggregateTPS,
			&e.RequestsPerSecond,
			&e.AcceleratorUtilizationPct, &e.AcceleratorMemoryPeakGiB,
		)
		if err != nil {
			return nil, fmt.Errorf("scan catalog row: %w", err)
		}
		entries = append(entries, e)
	}
	return entries, rows.Err()
}
