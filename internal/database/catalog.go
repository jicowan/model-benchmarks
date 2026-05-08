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
	ModelType            *string  `json:"model_type,omitempty"`
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
	TTFTP95Ms                *float64 `json:"ttft_p95_ms,omitempty"`
	TTFTP99Ms                *float64 `json:"ttft_p99_ms,omitempty"`
	E2ELatencyP50Ms          *float64 `json:"e2e_latency_p50_ms,omitempty"`
	E2ELatencyP95Ms          *float64 `json:"e2e_latency_p95_ms,omitempty"`
	E2ELatencyP99Ms          *float64 `json:"e2e_latency_p99_ms,omitempty"`
	ITLP50Ms                 *float64 `json:"itl_p50_ms,omitempty"`
	ITLP95Ms                 *float64 `json:"itl_p95_ms,omitempty"`
	ITLP99Ms                 *float64 `json:"itl_p99_ms,omitempty"`
	ThroughputPerRequestTPS  *float64 `json:"throughput_per_request_tps,omitempty"`
	ThroughputAggregateTPS   *float64 `json:"throughput_aggregate_tps,omitempty"`
	RequestsPerSecond        *float64 `json:"requests_per_second,omitempty"`
	SuccessfulRequests       *int     `json:"successful_requests,omitempty"`
	FailedRequests           *int     `json:"failed_requests,omitempty"`
	AcceleratorUtilizationPct    *float64 `json:"accelerator_utilization_pct,omitempty"`
	AcceleratorUtilizationAvgPct *float64 `json:"accelerator_utilization_avg_pct,omitempty"`
	AcceleratorMemoryPeakGiB     *float64 `json:"accelerator_memory_peak_gib,omitempty"`
	AcceleratorMemoryAvgGiB      *float64 `json:"accelerator_memory_avg_gib,omitempty"`
	// PRD-22: DCP GPU metrics
	SMActiveAvgPct     *float64 `json:"sm_active_avg_pct,omitempty"`
	TensorActiveAvgPct *float64 `json:"tensor_active_avg_pct,omitempty"`
	DRAMActiveAvgPct   *float64 `json:"dram_active_avg_pct,omitempty"`
}

// CatalogFilter holds optional filters for catalog queries.
type CatalogFilter struct {
	RunIDs          []string // exact match on br.id (used by Compare)
	ModelHfID       string   // substring ILIKE on model hf_id (UI is a free-text search)
	ModelType       string   // exact match on model_type (HF architecture name)
	InstanceFamily  string   // exact match on instance family (e.g. "p5")
	AcceleratorType string   // "gpu" or "neuron"
	SortBy          string   // column name to sort by
	SortDesc        bool     // true for descending sort
	Limit           int      // max results (0 = default 100)
	Offset          int      // pagination offset
}

// allowedSortColumns maps user-facing sort keys to SQL column expressions.
// Columns reference the `catalog_rows` materialized view (PRD-37); see
// db/migrations/025_catalog_materialized.sql for its schema.
var allowedSortColumns = map[string]string{
	"model":                       "hf_id",
	"instance":                    "instance_type_name",
	"ttft_p50":                    "ttft_p50_ms",
	"ttft_p95":                    "ttft_p95_ms",
	"ttft_p99":                    "ttft_p99_ms",
	"e2e_latency_p50":             "e2e_latency_p50_ms",
	"e2e_latency_p95":             "e2e_latency_p95_ms",
	"e2e_latency_p99":             "e2e_latency_p99_ms",
	"itl_p50":                     "itl_p50_ms",
	"itl_p95":                     "itl_p95_ms",
	"itl_p99":                     "itl_p99_ms",
	"throughput_per_request":      "throughput_per_request_tps",
	"throughput_aggregate":        "throughput_aggregate_tps",
	"requests_per_second":         "requests_per_second",
	"accelerator_utilization":     "accelerator_utilization_pct",
	"accelerator_utilization_avg": "accelerator_utilization_avg_pct",
	"accelerator_memory_peak":     "accelerator_memory_peak_gib",
	"accelerator_memory_avg":      "accelerator_memory_avg_gib",
	"sm_active_avg":               "sm_active_avg_pct",
	"tensor_active_avg":           "tensor_active_avg_pct",
	"dram_active_avg":             "dram_active_avg_pct",
	"completed_at":                "completed_at",
}

// ListCatalog queries the catalog with optional filters and sorting. Returns
// the page plus the total number of matching rows so the UI can render
// "showing X-Y of Z" without a second query.
func (r *Repository) ListCatalog(ctx context.Context, f CatalogFilter) ([]CatalogEntry, int, error) {
	var (
		conditions []string
		args       []any
		argIdx     int
	)

	// The `catalog_rows` materialized view already bakes in
	// status='completed' AND superseded=FALSE (PRD-37), so no filter is
	// needed here.

	if len(f.RunIDs) > 0 {
		argIdx++
		// pgx accepts []string → ANY($N) so the Compare page can pass its
		// 2-4 selected IDs in a single round-trip.
		conditions = append(conditions, fmt.Sprintf("run_id = ANY($%d)", argIdx))
		args = append(args, f.RunIDs)
	}
	if f.ModelHfID != "" {
		argIdx++
		conditions = append(conditions, fmt.Sprintf("hf_id ILIKE $%d", argIdx))
		args = append(args, "%"+f.ModelHfID+"%")
	}
	if f.ModelType != "" {
		argIdx++
		conditions = append(conditions, fmt.Sprintf("model_type = $%d", argIdx))
		args = append(args, f.ModelType)
	}
	if f.InstanceFamily != "" {
		argIdx++
		conditions = append(conditions, fmt.Sprintf("instance_family = $%d", argIdx))
		args = append(args, f.InstanceFamily)
	}
	if f.AcceleratorType != "" {
		argIdx++
		conditions = append(conditions, fmt.Sprintf("accelerator_type = $%d", argIdx))
		args = append(args, f.AcceleratorType)
	}

	where := ""
	if len(conditions) > 0 {
		where = "WHERE " + strings.Join(conditions, " AND ")
	}

	// Sort.
	orderBy := "ORDER BY hf_id, instance_type_name"
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
			run_id, hf_id, model_type, parameter_count,
			instance_type_name, instance_family, accelerator_type, accelerator_name,
			accelerator_count, accelerator_memory_gib,
			framework, framework_version, tensor_parallel_degree,
			quantization, concurrency,
			input_sequence_length, output_sequence_length,
			completed_at,
			ttft_p50_ms, ttft_p95_ms, ttft_p99_ms,
			e2e_latency_p50_ms, e2e_latency_p95_ms, e2e_latency_p99_ms,
			itl_p50_ms, itl_p95_ms, itl_p99_ms,
			throughput_per_request_tps, throughput_aggregate_tps,
			requests_per_second,
			successful_requests, failed_requests,
			accelerator_utilization_pct, accelerator_utilization_avg_pct,
			accelerator_memory_peak_gib, accelerator_memory_avg_gib,
			sm_active_avg_pct, tensor_active_avg_pct,
			dram_active_avg_pct,
			COUNT(*) OVER () AS total_count
		FROM catalog_rows
		%s
		%s
		%s %s
	`, where, orderBy, limitClause, offsetClause)

	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("query catalog: %w", err)
	}
	defer rows.Close()

	var (
		entries []CatalogEntry
		total   int
	)
	for rows.Next() {
		var e CatalogEntry
		err := rows.Scan(
			&e.RunID, &e.ModelHfID, &e.ModelType, &e.ParameterCount,
			&e.InstanceTypeName, &e.InstanceFamily, &e.AcceleratorType, &e.AcceleratorName,
			&e.AcceleratorCount, &e.AcceleratorMemoryGiB,
			&e.Framework, &e.FrameworkVersion, &e.TensorParallelDegree,
			&e.Quantization, &e.Concurrency,
			&e.InputSequenceLength, &e.OutputSequenceLength,
			&e.CompletedAt,
			&e.TTFTP50Ms, &e.TTFTP95Ms, &e.TTFTP99Ms,
			&e.E2ELatencyP50Ms, &e.E2ELatencyP95Ms, &e.E2ELatencyP99Ms,
			&e.ITLP50Ms, &e.ITLP95Ms, &e.ITLP99Ms,
			&e.ThroughputPerRequestTPS, &e.ThroughputAggregateTPS,
			&e.RequestsPerSecond,
			&e.SuccessfulRequests, &e.FailedRequests,
			&e.AcceleratorUtilizationPct, &e.AcceleratorUtilizationAvgPct,
			&e.AcceleratorMemoryPeakGiB, &e.AcceleratorMemoryAvgGiB,
			&e.SMActiveAvgPct, &e.TensorActiveAvgPct,
			&e.DRAMActiveAvgPct,
			&total,
		)
		if err != nil {
			return nil, 0, fmt.Errorf("scan catalog row: %w", err)
		}
		entries = append(entries, e)
	}
	return entries, total, rows.Err()
}
