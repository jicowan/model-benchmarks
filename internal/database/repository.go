package database

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// extractModelFamily extracts the model family from a HuggingFace model ID.
// Returns one of: llama, mistral, qwen, deepseek, gemma, phi, or empty string.
// Priority: check organization name first (before /), then model name.
func extractModelFamily(hfID string) string {
	lower := strings.ToLower(hfID)
	families := []string{"llama", "mistral", "qwen", "deepseek", "gemma", "phi"}

	// Split into org and model name
	parts := strings.SplitN(lower, "/", 2)
	org := parts[0]

	// Check organization name first (more reliable)
	for _, f := range families {
		if strings.Contains(org, f) {
			return f
		}
	}

	// Fall back to checking full ID (for cases like TinyLlama/...)
	for _, f := range families {
		if strings.Contains(lower, f) {
			return f
		}
	}
	return ""
}

// Repository provides database operations for benchmark data.
type Repository struct {
	pool *pgxpool.Pool
}

// NewRepository creates a new Repository with a connection pool.
func NewRepository(ctx context.Context, connString string) (*Repository, error) {
	pool, err := pgxpool.New(ctx, connString)
	if err != nil {
		return nil, fmt.Errorf("create connection pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}
	return &Repository{pool: pool}, nil
}

// Close closes the connection pool.
func (r *Repository) Close() {
	r.pool.Close()
}

// Ping verifies the database connection is healthy.
func (r *Repository) Ping(ctx context.Context) error {
	return r.pool.Ping(ctx)
}

// GetModelByHfID returns a model by its Hugging Face ID and revision, or nil if not found.
func (r *Repository) GetModelByHfID(ctx context.Context, hfID, hfRevision string) (*Model, error) {
	var m Model
	err := r.pool.QueryRow(ctx,
		`SELECT id, hf_id, hf_revision, model_family, parameter_count, created_at
		 FROM models WHERE hf_id = $1 AND hf_revision = $2`, hfID, hfRevision,
	).Scan(&m.ID, &m.HfID, &m.HfRevision, &m.ModelFamily, &m.ParameterCount, &m.CreatedAt)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("query model: %w", err)
	}
	return &m, nil
}

// EnsureModel returns an existing model or creates one if it doesn't exist.
func (r *Repository) EnsureModel(ctx context.Context, hfID, hfRevision string) (*Model, error) {
	m, err := r.GetModelByHfID(ctx, hfID, hfRevision)
	if err != nil {
		return nil, err
	}
	if m != nil {
		return m, nil
	}

	// Extract model family from HuggingFace ID
	family := extractModelFamily(hfID)
	var familyPtr *string
	if family != "" {
		familyPtr = &family
	}

	var created Model
	err = r.pool.QueryRow(ctx,
		`INSERT INTO models (hf_id, hf_revision, model_family)
		 VALUES ($1, $2, $3)
		 ON CONFLICT (hf_id, hf_revision) DO UPDATE SET model_family = COALESCE(models.model_family, EXCLUDED.model_family)
		 RETURNING id, hf_id, hf_revision, model_family, parameter_count, created_at`,
		hfID, hfRevision, familyPtr,
	).Scan(&created.ID, &created.HfID, &created.HfRevision, &created.ModelFamily, &created.ParameterCount, &created.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("upsert model: %w", err)
	}
	return &created, nil
}

// BackfillModelFamilies updates model_family for all models using the same
// priority logic as extractModelFamily (org takes priority over model name).
func (r *Repository) BackfillModelFamilies(ctx context.Context) (int64, error) {
	var totalUpdated int64

	// First pass: match by organization (higher priority)
	// Process in order so that more specific orgs are checked first
	families := []string{"deepseek", "llama", "mistral", "qwen", "gemma", "phi"}
	for _, family := range families {
		pattern := "%" + family + "%"
		result, err := r.pool.Exec(ctx,
			`UPDATE models SET model_family = $1
			 WHERE model_family IS NULL
			   AND LOWER(SPLIT_PART(hf_id, '/', 1)) LIKE $2`,
			family, pattern,
		)
		if err != nil {
			return totalUpdated, fmt.Errorf("backfill org %s: %w", family, err)
		}
		totalUpdated += result.RowsAffected()
	}

	// Second pass: match by full ID (for remaining NULL values)
	for _, family := range families {
		pattern := "%" + family + "%"
		result, err := r.pool.Exec(ctx,
			`UPDATE models SET model_family = $1
			 WHERE model_family IS NULL AND LOWER(hf_id) LIKE $2`,
			family, pattern,
		)
		if err != nil {
			return totalUpdated, fmt.Errorf("backfill full %s: %w", family, err)
		}
		totalUpdated += result.RowsAffected()
	}

	return totalUpdated, nil
}

// GetInstanceTypeByName returns an instance type by name, or nil if not found.
func (r *Repository) GetInstanceTypeByName(ctx context.Context, name string) (*InstanceType, error) {
	var it InstanceType
	err := r.pool.QueryRow(ctx,
		`SELECT id, name, family, accelerator_type, accelerator_name,
		        accelerator_count, accelerator_memory_gib, vcpus, memory_gib
		 FROM instance_types WHERE name = $1`, name,
	).Scan(&it.ID, &it.Name, &it.Family, &it.AcceleratorType, &it.AcceleratorName,
		&it.AcceleratorCount, &it.AcceleratorMemoryGiB, &it.VCPUs, &it.MemoryGiB)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("query instance type: %w", err)
	}
	return &it, nil
}

// CreateBenchmarkRun inserts a new benchmark run and returns its ID.
func (r *Repository) CreateBenchmarkRun(ctx context.Context, run *BenchmarkRun) (string, error) {
	var id string
	err := r.pool.QueryRow(ctx,
		`INSERT INTO benchmark_runs
		    (model_id, instance_type_id, framework, framework_version,
		     tensor_parallel_degree, quantization, concurrency,
		     input_sequence_length, output_sequence_length, dataset_name,
		     run_type, status, min_duration_seconds, max_model_len, scenario_id)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)
		 RETURNING id`,
		run.ModelID, run.InstanceTypeID, run.Framework, run.FrameworkVersion,
		run.TensorParallelDegree, run.Quantization, run.Concurrency,
		run.InputSequenceLength, run.OutputSequenceLength, run.DatasetName,
		run.RunType, run.Status, run.MinDurationSeconds, nullableInt(run.MaxModelLen),
		run.ScenarioID,
	).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("insert benchmark run: %w", err)
	}
	return id, nil
}

// nullableInt returns nil if v is 0, otherwise returns a pointer to v.
func nullableInt(v int) *int {
	if v == 0 {
		return nil
	}
	return &v
}

// UpdateRunStatus updates the status and optional timestamps of a benchmark run.
func (r *Repository) UpdateRunStatus(ctx context.Context, runID, status string) error {
	var query string
	switch status {
	case "running":
		query = `UPDATE benchmark_runs SET status = $1, started_at = $2 WHERE id = $3`
	case "completed", "failed":
		query = `UPDATE benchmark_runs SET status = $1, completed_at = $2 WHERE id = $3`
	default:
		query = `UPDATE benchmark_runs SET status = $1 WHERE id = $2`
		_, err := r.pool.Exec(ctx, query, status, runID)
		return err
	}
	_, err := r.pool.Exec(ctx, query, status, time.Now(), runID)
	if err != nil {
		return fmt.Errorf("update run status: %w", err)
	}
	return nil
}

// UpdateRunFailed sets a run's status to "failed" with an error message explaining why.
func (r *Repository) UpdateRunFailed(ctx context.Context, runID, reason string) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE benchmark_runs SET status = 'failed', error_message = $1, completed_at = $2 WHERE id = $3`,
		reason, time.Now(), runID)
	if err != nil {
		return fmt.Errorf("update run failed: %w", err)
	}
	return nil
}

// UpdateLoadgenConfig stores the inference-perf configuration YAML for a benchmark run.
func (r *Repository) UpdateLoadgenConfig(ctx context.Context, runID, config string) error {
	_, err := r.pool.Exec(ctx, `UPDATE benchmark_runs SET loadgen_config = $1 WHERE id = $2`, config, runID)
	if err != nil {
		return fmt.Errorf("update loadgen config: %w", err)
	}
	return nil
}

// SetLoadgenStartedAt records when the load generator job started.
func (r *Repository) SetLoadgenStartedAt(ctx context.Context, runID string) error {
	_, err := r.pool.Exec(ctx, `UPDATE benchmark_runs SET loadgen_started_at = $1 WHERE id = $2`, time.Now(), runID)
	if err != nil {
		return fmt.Errorf("set loadgen started: %w", err)
	}
	return nil
}

// GetLoadgenStartedAt returns the loadgen_started_at timestamp for a run.
func (r *Repository) GetLoadgenStartedAt(ctx context.Context, runID string) (*time.Time, error) {
	var t *time.Time
	err := r.pool.QueryRow(ctx, `SELECT loadgen_started_at FROM benchmark_runs WHERE id = $1`, runID).Scan(&t)
	if err != nil {
		return nil, fmt.Errorf("get loadgen started: %w", err)
	}
	return t, nil
}

// PersistMetrics inserts benchmark metrics and marks the run as completed
// within a single transaction. It verifies the write by reading back the
// inserted metrics row before committing.
func (r *Repository) PersistMetrics(ctx context.Context, runID string, m *BenchmarkMetrics) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	// Calculate duration from loadgen_started_at if not already set.
	if m.TotalDurationSeconds == nil || *m.TotalDurationSeconds == 0 {
		var loadgenStartedAt *time.Time
		_ = tx.QueryRow(ctx, `SELECT loadgen_started_at FROM benchmark_runs WHERE id = $1`, runID).Scan(&loadgenStartedAt)
		if loadgenStartedAt != nil {
			dur := time.Since(*loadgenStartedAt).Seconds()
			m.TotalDurationSeconds = &dur
		}
	}

	// Insert metrics.
	var metricsID string
	err = tx.QueryRow(ctx,
		`INSERT INTO benchmark_metrics
		    (run_id,
		     ttft_p50_ms, ttft_p90_ms, ttft_p95_ms, ttft_p99_ms,
		     e2e_latency_p50_ms, e2e_latency_p90_ms, e2e_latency_p95_ms, e2e_latency_p99_ms,
		     itl_p50_ms, itl_p90_ms, itl_p95_ms, itl_p99_ms,
		     throughput_per_request_tps, throughput_aggregate_tps, requests_per_second,
		     accelerator_utilization_pct, accelerator_utilization_avg_pct,
		     accelerator_memory_peak_gib, accelerator_memory_avg_gib,
		     waiting_requests_max,
		     successful_requests, failed_requests, total_duration_seconds,
		     tpot_p50_ms, tpot_p90_ms, tpot_p99_ms,
		     prefill_time_p50_ms, decode_time_p50_ms, queue_time_p50_ms,
		     prompt_throughput_tps, generation_throughput_tps,
		     kv_cache_utilization_avg_pct, kv_cache_utilization_peak_pct,
		     prefix_cache_hit_rate, preemption_count,
		     running_requests_avg, running_requests_max, output_length_mean,
		     sm_active_avg_pct, sm_active_peak_pct,
		     tensor_active_avg_pct, tensor_active_peak_pct,
		     dram_active_avg_pct, dram_active_peak_pct)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,
		         $24,$25,$26,$27,$28,$29,$30,$31,$32,$33,$34,$35,$36,$37,$38,$39,$40,$41,$42,$43,$44,$45)
		 RETURNING id`,
		runID,
		m.TTFTP50Ms, m.TTFTP90Ms, m.TTFTP95Ms, m.TTFTP99Ms,
		m.E2ELatencyP50Ms, m.E2ELatencyP90Ms, m.E2ELatencyP95Ms, m.E2ELatencyP99Ms,
		m.ITLP50Ms, m.ITLP90Ms, m.ITLP95Ms, m.ITLP99Ms,
		m.ThroughputPerRequestTPS, m.ThroughputAggregateTPS, m.RequestsPerSecond,
		m.AcceleratorUtilizationPct, m.AcceleratorUtilizationAvgPct,
		m.AcceleratorMemoryPeakGiB, m.AcceleratorMemoryAvgGiB,
		m.WaitingRequestsMax,
		m.SuccessfulRequests, m.FailedRequests, m.TotalDurationSeconds,
		m.TPOTP50Ms, m.TPOTP90Ms, m.TPOTP99Ms,
		m.PrefillTimeP50Ms, m.DecodeTimeP50Ms, m.QueueTimeP50Ms,
		m.PromptThroughputTPS, m.GenerationThroughputTPS,
		m.KVCacheUtilizationAvgPct, m.KVCacheUtilizationPeakPct,
		m.PrefixCacheHitRate, m.PreemptionCount,
		m.RunningRequestsAvg, m.RunningRequestsMax, m.OutputLengthMean,
		m.SMActiveAvgPct, m.SMActivePeakPct,
		m.TensorActiveAvgPct, m.TensorActivePeakPct,
		m.DRAMActiveAvgPct, m.DRAMActivePeakPct,
	).Scan(&metricsID)
	if err != nil {
		return fmt.Errorf("insert metrics: %w", err)
	}

	// Verify the write by reading it back.
	var verifyRunID string
	err = tx.QueryRow(ctx,
		`SELECT run_id FROM benchmark_metrics WHERE id = $1`, metricsID,
	).Scan(&verifyRunID)
	if err != nil {
		return fmt.Errorf("verify metrics write: %w", err)
	}
	if verifyRunID != runID {
		return fmt.Errorf("metrics verification failed: expected run_id %s, got %s", runID, verifyRunID)
	}

	// Mark run as completed.
	_, err = tx.Exec(ctx,
		`UPDATE benchmark_runs SET status = 'completed', completed_at = $1 WHERE id = $2`,
		time.Now(), runID,
	)
	if err != nil {
		return fmt.Errorf("update run to completed: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	return nil
}

// GetBenchmarkRun returns a benchmark run by ID.
func (r *Repository) GetBenchmarkRun(ctx context.Context, runID string) (*BenchmarkRun, error) {
	var run BenchmarkRun
	var maxModelLen *int
	err := r.pool.QueryRow(ctx,
		`SELECT id, model_id, instance_type_id, framework, framework_version,
		        tensor_parallel_degree, quantization, concurrency,
		        input_sequence_length, output_sequence_length, dataset_name,
		        run_type, min_duration_seconds, max_model_len, status, error_message, superseded,
		        started_at, loadgen_started_at, completed_at, created_at
		 FROM benchmark_runs WHERE id = $1`, runID,
	).Scan(&run.ID, &run.ModelID, &run.InstanceTypeID, &run.Framework, &run.FrameworkVersion,
		&run.TensorParallelDegree, &run.Quantization, &run.Concurrency,
		&run.InputSequenceLength, &run.OutputSequenceLength, &run.DatasetName,
		&run.RunType, &run.MinDurationSeconds, &maxModelLen, &run.Status, &run.ErrorMessage, &run.Superseded,
		&run.StartedAt, &run.LoadgenStartedAt, &run.CompletedAt, &run.CreatedAt)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("query benchmark run: %w", err)
	}
	if maxModelLen != nil {
		run.MaxModelLen = *maxModelLen
	}
	return &run, nil
}

// GetRunsByStatus returns all benchmark runs with the given status.
func (r *Repository) GetRunsByStatus(ctx context.Context, status string) ([]BenchmarkRun, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT id, model_id, instance_type_id, framework, framework_version,
		        tensor_parallel_degree, quantization, concurrency,
		        input_sequence_length, output_sequence_length, dataset_name,
		        run_type, min_duration_seconds, max_model_len, status, error_message, superseded,
		        started_at, loadgen_started_at, completed_at, created_at
		 FROM benchmark_runs WHERE status = $1`, status,
	)
	if err != nil {
		return nil, fmt.Errorf("query runs by status: %w", err)
	}
	defer rows.Close()

	var runs []BenchmarkRun
	for rows.Next() {
		var run BenchmarkRun
		var maxModelLen *int
		if err := rows.Scan(&run.ID, &run.ModelID, &run.InstanceTypeID, &run.Framework, &run.FrameworkVersion,
			&run.TensorParallelDegree, &run.Quantization, &run.Concurrency,
			&run.InputSequenceLength, &run.OutputSequenceLength, &run.DatasetName,
			&run.RunType, &run.MinDurationSeconds, &maxModelLen, &run.Status, &run.ErrorMessage, &run.Superseded,
			&run.StartedAt, &run.LoadgenStartedAt, &run.CompletedAt, &run.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan run: %w", err)
		}
		if maxModelLen != nil {
			run.MaxModelLen = *maxModelLen
		}
		runs = append(runs, run)
	}
	return runs, rows.Err()
}

// GetMetricsByRunID returns benchmark metrics for a given run.
func (r *Repository) GetMetricsByRunID(ctx context.Context, runID string) (*BenchmarkMetrics, error) {
	var m BenchmarkMetrics
	err := r.pool.QueryRow(ctx,
		`SELECT id, run_id,
		        ttft_p50_ms, ttft_p90_ms, ttft_p95_ms, ttft_p99_ms,
		        e2e_latency_p50_ms, e2e_latency_p90_ms, e2e_latency_p95_ms, e2e_latency_p99_ms,
		        itl_p50_ms, itl_p90_ms, itl_p95_ms, itl_p99_ms,
		        throughput_per_request_tps, throughput_aggregate_tps, requests_per_second,
		        accelerator_utilization_pct, accelerator_utilization_avg_pct,
		        accelerator_memory_peak_gib, accelerator_memory_avg_gib,
		        waiting_requests_max,
		        successful_requests, failed_requests, total_duration_seconds, created_at,
		        tpot_p50_ms, tpot_p90_ms, tpot_p99_ms,
		        prefill_time_p50_ms, decode_time_p50_ms, queue_time_p50_ms,
		        prompt_throughput_tps, generation_throughput_tps,
		        kv_cache_utilization_avg_pct, kv_cache_utilization_peak_pct,
		        prefix_cache_hit_rate, preemption_count,
		        running_requests_avg, running_requests_max, output_length_mean,
		        sm_active_avg_pct, sm_active_peak_pct,
		        tensor_active_avg_pct, tensor_active_peak_pct,
		        dram_active_avg_pct, dram_active_peak_pct
		 FROM benchmark_metrics WHERE run_id = $1`, runID,
	).Scan(&m.ID, &m.RunID,
		&m.TTFTP50Ms, &m.TTFTP90Ms, &m.TTFTP95Ms, &m.TTFTP99Ms,
		&m.E2ELatencyP50Ms, &m.E2ELatencyP90Ms, &m.E2ELatencyP95Ms, &m.E2ELatencyP99Ms,
		&m.ITLP50Ms, &m.ITLP90Ms, &m.ITLP95Ms, &m.ITLP99Ms,
		&m.ThroughputPerRequestTPS, &m.ThroughputAggregateTPS, &m.RequestsPerSecond,
		&m.AcceleratorUtilizationPct, &m.AcceleratorUtilizationAvgPct,
		&m.AcceleratorMemoryPeakGiB, &m.AcceleratorMemoryAvgGiB,
		&m.WaitingRequestsMax,
		&m.SuccessfulRequests, &m.FailedRequests, &m.TotalDurationSeconds, &m.CreatedAt,
		&m.TPOTP50Ms, &m.TPOTP90Ms, &m.TPOTP99Ms,
		&m.PrefillTimeP50Ms, &m.DecodeTimeP50Ms, &m.QueueTimeP50Ms,
		&m.PromptThroughputTPS, &m.GenerationThroughputTPS,
		&m.KVCacheUtilizationAvgPct, &m.KVCacheUtilizationPeakPct,
		&m.PrefixCacheHitRate, &m.PreemptionCount,
		&m.RunningRequestsAvg, &m.RunningRequestsMax, &m.OutputLengthMean,
		&m.SMActiveAvgPct, &m.SMActivePeakPct,
		&m.TensorActiveAvgPct, &m.TensorActivePeakPct,
		&m.DRAMActiveAvgPct, &m.DRAMActivePeakPct)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("query metrics: %w", err)
	}
	return &m, nil
}
