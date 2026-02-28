package database

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

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
	var created Model
	err = r.pool.QueryRow(ctx,
		`INSERT INTO models (hf_id, hf_revision)
		 VALUES ($1, $2)
		 ON CONFLICT (hf_id, hf_revision) DO UPDATE SET hf_id = EXCLUDED.hf_id
		 RETURNING id, hf_id, hf_revision, model_family, parameter_count, created_at`,
		hfID, hfRevision,
	).Scan(&created.ID, &created.HfID, &created.HfRevision, &created.ModelFamily, &created.ParameterCount, &created.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("upsert model: %w", err)
	}
	return &created, nil
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
		     run_type, status)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
		 RETURNING id`,
		run.ModelID, run.InstanceTypeID, run.Framework, run.FrameworkVersion,
		run.TensorParallelDegree, run.Quantization, run.Concurrency,
		run.InputSequenceLength, run.OutputSequenceLength, run.DatasetName,
		run.RunType, run.Status,
	).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("insert benchmark run: %w", err)
	}
	return id, nil
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

// PersistMetrics inserts benchmark metrics and marks the run as completed
// within a single transaction. It verifies the write by reading back the
// inserted metrics row before committing.
func (r *Repository) PersistMetrics(ctx context.Context, runID string, m *BenchmarkMetrics) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	// Insert metrics.
	var metricsID string
	err = tx.QueryRow(ctx,
		`INSERT INTO benchmark_metrics
		    (run_id,
		     ttft_p50_ms, ttft_p90_ms, ttft_p95_ms, ttft_p99_ms,
		     e2e_latency_p50_ms, e2e_latency_p90_ms, e2e_latency_p95_ms, e2e_latency_p99_ms,
		     itl_p50_ms, itl_p90_ms, itl_p95_ms, itl_p99_ms,
		     throughput_per_request_tps, throughput_aggregate_tps, requests_per_second,
		     accelerator_utilization_pct, accelerator_memory_peak_gib,
		     successful_requests, failed_requests, total_duration_seconds)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21)
		 RETURNING id`,
		runID,
		m.TTFTP50Ms, m.TTFTP90Ms, m.TTFTP95Ms, m.TTFTP99Ms,
		m.E2ELatencyP50Ms, m.E2ELatencyP90Ms, m.E2ELatencyP95Ms, m.E2ELatencyP99Ms,
		m.ITLP50Ms, m.ITLP90Ms, m.ITLP95Ms, m.ITLP99Ms,
		m.ThroughputPerRequestTPS, m.ThroughputAggregateTPS, m.RequestsPerSecond,
		m.AcceleratorUtilizationPct, m.AcceleratorMemoryPeakGiB,
		m.SuccessfulRequests, m.FailedRequests, m.TotalDurationSeconds,
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
	err := r.pool.QueryRow(ctx,
		`SELECT id, model_id, instance_type_id, framework, framework_version,
		        tensor_parallel_degree, quantization, concurrency,
		        input_sequence_length, output_sequence_length, dataset_name,
		        run_type, status, superseded, started_at, completed_at, created_at
		 FROM benchmark_runs WHERE id = $1`, runID,
	).Scan(&run.ID, &run.ModelID, &run.InstanceTypeID, &run.Framework, &run.FrameworkVersion,
		&run.TensorParallelDegree, &run.Quantization, &run.Concurrency,
		&run.InputSequenceLength, &run.OutputSequenceLength, &run.DatasetName,
		&run.RunType, &run.Status, &run.Superseded, &run.StartedAt, &run.CompletedAt, &run.CreatedAt)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("query benchmark run: %w", err)
	}
	return &run, nil
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
		        accelerator_utilization_pct, accelerator_memory_peak_gib,
		        successful_requests, failed_requests, total_duration_seconds, created_at
		 FROM benchmark_metrics WHERE run_id = $1`, runID,
	).Scan(&m.ID, &m.RunID,
		&m.TTFTP50Ms, &m.TTFTP90Ms, &m.TTFTP95Ms, &m.TTFTP99Ms,
		&m.E2ELatencyP50Ms, &m.E2ELatencyP90Ms, &m.E2ELatencyP95Ms, &m.E2ELatencyP99Ms,
		&m.ITLP50Ms, &m.ITLP90Ms, &m.ITLP95Ms, &m.ITLP99Ms,
		&m.ThroughputPerRequestTPS, &m.ThroughputAggregateTPS, &m.RequestsPerSecond,
		&m.AcceleratorUtilizationPct, &m.AcceleratorMemoryPeakGiB,
		&m.SuccessfulRequests, &m.FailedRequests, &m.TotalDurationSeconds, &m.CreatedAt)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("query metrics: %w", err)
	}
	return &m, nil
}
