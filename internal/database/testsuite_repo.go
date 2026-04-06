package database

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// CreateTestSuiteRun creates a new test suite run and returns its ID.
func (r *Repository) CreateTestSuiteRun(ctx context.Context, run *TestSuiteRun) (string, error) {
	var id string
	err := r.pool.QueryRow(ctx,
		`INSERT INTO test_suite_runs
		    (model_id, instance_type_id, suite_id, tensor_parallel_degree,
		     quantization, max_model_len, status)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)
		 RETURNING id`,
		run.ModelID, run.InstanceTypeID, run.SuiteID, run.TensorParallelDegree,
		run.Quantization, nullableInt(run.MaxModelLen), run.Status,
	).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("insert test suite run: %w", err)
	}
	return id, nil
}

// GetTestSuiteRun retrieves a test suite run by ID.
func (r *Repository) GetTestSuiteRun(ctx context.Context, id string) (*TestSuiteRun, error) {
	var run TestSuiteRun
	err := r.pool.QueryRow(ctx,
		`SELECT id, model_id, instance_type_id, suite_id, tensor_parallel_degree,
		        quantization, max_model_len, status, current_scenario,
		        started_at, completed_at, created_at
		 FROM test_suite_runs WHERE id = $1`, id,
	).Scan(&run.ID, &run.ModelID, &run.InstanceTypeID, &run.SuiteID,
		&run.TensorParallelDegree, &run.Quantization, &run.MaxModelLen,
		&run.Status, &run.CurrentScenario, &run.StartedAt, &run.CompletedAt, &run.CreatedAt)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get test suite run: %w", err)
	}
	return &run, nil
}

// UpdateSuiteRunStatus updates the status and current scenario of a test suite run.
func (r *Repository) UpdateSuiteRunStatus(ctx context.Context, id, status string, currentScenario *string) error {
	var query string
	var args []any

	switch status {
	case "running":
		query = `UPDATE test_suite_runs SET status = $1, current_scenario = $2, started_at = $3 WHERE id = $4`
		args = []any{status, currentScenario, time.Now(), id}
	case "completed", "failed":
		query = `UPDATE test_suite_runs SET status = $1, current_scenario = $2, completed_at = $3 WHERE id = $4`
		args = []any{status, currentScenario, time.Now(), id}
	default:
		query = `UPDATE test_suite_runs SET status = $1, current_scenario = $2 WHERE id = $3`
		args = []any{status, currentScenario, id}
	}

	_, err := r.pool.Exec(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("update suite run status: %w", err)
	}
	return nil
}

// CreateScenarioResult creates a new scenario result record.
func (r *Repository) CreateScenarioResult(ctx context.Context, result *ScenarioResult) (string, error) {
	var id string
	err := r.pool.QueryRow(ctx,
		`INSERT INTO scenario_results (suite_run_id, scenario_id, status)
		 VALUES ($1, $2, $3)
		 RETURNING id`,
		result.SuiteRunID, result.ScenarioID, result.Status,
	).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("insert scenario result: %w", err)
	}
	return id, nil
}

// UpdateScenarioResult updates an existing scenario result with metrics.
func (r *Repository) UpdateScenarioResult(ctx context.Context, result *ScenarioResult) error {
	var query string
	var args []any

	switch result.Status {
	case "running":
		query = `UPDATE scenario_results SET status = $1, started_at = $2 WHERE id = $3`
		args = []any{result.Status, time.Now(), result.ID}
	case "completed":
		query = `UPDATE scenario_results SET
		    status = $1, completed_at = $2,
		    ttft_p50_ms = $3, ttft_p90_ms = $4, ttft_p99_ms = $5,
		    e2e_latency_p50_ms = $6, e2e_latency_p90_ms = $7, e2e_latency_p99_ms = $8,
		    itl_p50_ms = $9, itl_p90_ms = $10, itl_p99_ms = $11,
		    throughput_tps = $12, requests_per_second = $13,
		    successful_requests = $14, failed_requests = $15,
		    accelerator_utilization_pct = $16, accelerator_memory_peak_gib = $17,
		    loadgen_config = $18
		 WHERE id = $19`
		args = []any{
			result.Status, time.Now(),
			result.TTFTP50Ms, result.TTFTP90Ms, result.TTFTP99Ms,
			result.E2ELatencyP50Ms, result.E2ELatencyP90Ms, result.E2ELatencyP99Ms,
			result.ITLP50Ms, result.ITLP90Ms, result.ITLP99Ms,
			result.ThroughputTPS, result.RequestsPerSecond,
			result.SuccessfulRequests, result.FailedRequests,
			result.AcceleratorUtilizationPct, result.AcceleratorMemoryPeakGiB,
			result.LoadgenConfig,
			result.ID,
		}
	case "failed":
		query = `UPDATE scenario_results SET status = $1, completed_at = $2, error_message = $3 WHERE id = $4`
		args = []any{result.Status, time.Now(), result.ErrorMessage, result.ID}
	default:
		query = `UPDATE scenario_results SET status = $1 WHERE id = $2`
		args = []any{result.Status, result.ID}
	}

	_, err := r.pool.Exec(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("update scenario result: %w", err)
	}
	return nil
}

// GetScenarioResults retrieves all scenario results for a suite run.
func (r *Repository) GetScenarioResults(ctx context.Context, suiteRunID string) ([]ScenarioResult, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT id, suite_run_id, scenario_id, status, error_message,
		        started_at, completed_at,
		        ttft_p50_ms, ttft_p90_ms, ttft_p99_ms,
		        e2e_latency_p50_ms, e2e_latency_p90_ms, e2e_latency_p99_ms,
		        itl_p50_ms, itl_p90_ms, itl_p99_ms,
		        throughput_tps, requests_per_second,
		        successful_requests, failed_requests,
		        accelerator_utilization_pct, accelerator_memory_peak_gib,
		        loadgen_config, created_at
		 FROM scenario_results
		 WHERE suite_run_id = $1
		 ORDER BY created_at ASC`, suiteRunID)
	if err != nil {
		return nil, fmt.Errorf("query scenario results: %w", err)
	}
	defer rows.Close()

	var results []ScenarioResult
	for rows.Next() {
		var r ScenarioResult
		err := rows.Scan(
			&r.ID, &r.SuiteRunID, &r.ScenarioID, &r.Status, &r.ErrorMessage,
			&r.StartedAt, &r.CompletedAt,
			&r.TTFTP50Ms, &r.TTFTP90Ms, &r.TTFTP99Ms,
			&r.E2ELatencyP50Ms, &r.E2ELatencyP90Ms, &r.E2ELatencyP99Ms,
			&r.ITLP50Ms, &r.ITLP90Ms, &r.ITLP99Ms,
			&r.ThroughputTPS, &r.RequestsPerSecond,
			&r.SuccessfulRequests, &r.FailedRequests,
			&r.AcceleratorUtilizationPct, &r.AcceleratorMemoryPeakGiB,
			&r.LoadgenConfig, &r.CreatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("scan scenario result: %w", err)
		}
		results = append(results, r)
	}
	return results, nil
}

// ListTestSuiteRuns lists test suite runs, optionally filtered by model and instance type.
func (r *Repository) ListTestSuiteRuns(ctx context.Context, modelID, instanceTypeID string) ([]TestSuiteRun, error) {
	query := `SELECT id, model_id, instance_type_id, suite_id, tensor_parallel_degree,
	                 quantization, max_model_len, status, current_scenario,
	                 started_at, completed_at, created_at
	          FROM test_suite_runs WHERE 1=1`
	var args []any
	argNum := 1

	if modelID != "" {
		query += fmt.Sprintf(" AND model_id = $%d", argNum)
		args = append(args, modelID)
		argNum++
	}
	if instanceTypeID != "" {
		query += fmt.Sprintf(" AND instance_type_id = $%d", argNum)
		args = append(args, instanceTypeID)
		argNum++
	}
	query += " ORDER BY created_at DESC LIMIT 100"

	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query test suite runs: %w", err)
	}
	defer rows.Close()

	var runs []TestSuiteRun
	for rows.Next() {
		var run TestSuiteRun
		err := rows.Scan(
			&run.ID, &run.ModelID, &run.InstanceTypeID, &run.SuiteID,
			&run.TensorParallelDegree, &run.Quantization, &run.MaxModelLen,
			&run.Status, &run.CurrentScenario, &run.StartedAt, &run.CompletedAt, &run.CreatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("scan test suite run: %w", err)
		}
		runs = append(runs, run)
	}
	return runs, nil
}

// SuiteRunListItem is a denormalized row for the suite runs list.
type SuiteRunListItem struct {
	ID               string     `json:"id"`
	ModelHfID        string     `json:"model_hf_id"`
	InstanceTypeName string     `json:"instance_type_name"`
	SuiteID          string     `json:"suite_id"`
	Status           string     `json:"status"`
	CreatedAt        time.Time  `json:"created_at"`
	StartedAt        *time.Time `json:"started_at,omitempty"`
	CompletedAt      *time.Time `json:"completed_at,omitempty"`
}

// ListSuiteRunsWithNames returns suite runs with model and instance names joined.
func (r *Repository) ListSuiteRunsWithNames(ctx context.Context) ([]SuiteRunListItem, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT
			tsr.id, m.hf_id, it.name, tsr.suite_id, tsr.status,
			tsr.created_at, tsr.started_at, tsr.completed_at
		FROM test_suite_runs tsr
		JOIN models m ON tsr.model_id = m.id
		JOIN instance_types it ON tsr.instance_type_id = it.id
		ORDER BY tsr.created_at DESC
		LIMIT 100
	`)
	if err != nil {
		return nil, fmt.Errorf("query suite runs: %w", err)
	}
	defer rows.Close()

	var items []SuiteRunListItem
	for rows.Next() {
		var item SuiteRunListItem
		err := rows.Scan(
			&item.ID, &item.ModelHfID, &item.InstanceTypeName, &item.SuiteID,
			&item.Status, &item.CreatedAt, &item.StartedAt, &item.CompletedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("scan suite run: %w", err)
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// DeleteSuiteRun removes a test suite run and its associated scenario results.
func (r *Repository) DeleteSuiteRun(ctx context.Context, id string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `DELETE FROM scenario_results WHERE suite_run_id = $1`, id); err != nil {
		return fmt.Errorf("delete scenario results: %w", err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM test_suite_runs WHERE id = $1`, id); err != nil {
		return fmt.Errorf("delete suite run: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	return nil
}
