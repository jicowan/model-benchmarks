package database

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// RunFilter holds optional filters for listing benchmark runs.
type RunFilter struct {
	Status   string // "pending", "running", "completed", "failed", or ""
	ModelID  string // ILIKE filter on model hf_id
	Limit    int
	Offset   int
}

// RunListItem is a denormalized row for the jobs list.
type RunListItem struct {
	ID               string     `json:"id"`
	ModelHfID        string     `json:"model_hf_id"`
	InstanceTypeName string     `json:"instance_type_name"`
	Framework        string     `json:"framework"`
	RunType          string     `json:"run_type"`
	Status           string     `json:"status"`
	CreatedAt        time.Time  `json:"created_at"`
	StartedAt        *time.Time `json:"started_at,omitempty"`
	CompletedAt      *time.Time `json:"completed_at,omitempty"`
}

// ListRuns returns benchmark runs matching the given filter, joined with
// models and instance_types for display names.
func (r *Repository) ListRuns(ctx context.Context, f RunFilter) ([]RunListItem, error) {
	var (
		conditions []string
		args       []any
		argIdx     int
	)

	if f.Status != "" {
		argIdx++
		conditions = append(conditions, fmt.Sprintf("br.status = $%d", argIdx))
		args = append(args, f.Status)
	}
	if f.ModelID != "" {
		argIdx++
		conditions = append(conditions, fmt.Sprintf("m.hf_id ILIKE $%d", argIdx))
		args = append(args, "%"+f.ModelID+"%")
	}

	where := ""
	if len(conditions) > 0 {
		where = "WHERE " + strings.Join(conditions, " AND ")
	}

	// Pagination.
	limit := 50
	if f.Limit > 0 && f.Limit <= 200 {
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
			br.id, m.hf_id, it.name,
			br.framework, br.run_type, br.status,
			br.created_at, br.started_at, br.completed_at
		FROM benchmark_runs br
		JOIN models m ON br.model_id = m.id
		JOIN instance_types it ON br.instance_type_id = it.id
		%s
		ORDER BY br.created_at DESC
		%s %s
	`, where, limitClause, offsetClause)

	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query runs: %w", err)
	}
	defer rows.Close()

	var items []RunListItem
	for rows.Next() {
		var item RunListItem
		err := rows.Scan(
			&item.ID, &item.ModelHfID, &item.InstanceTypeName,
			&item.Framework, &item.RunType, &item.Status,
			&item.CreatedAt, &item.StartedAt, &item.CompletedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("scan run row: %w", err)
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// DeleteRun removes a benchmark run and its associated metrics.
func (r *Repository) DeleteRun(ctx context.Context, runID string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `DELETE FROM benchmark_metrics WHERE run_id = $1`, runID); err != nil {
		return fmt.Errorf("delete metrics: %w", err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM benchmark_runs WHERE id = $1`, runID); err != nil {
		return fmt.Errorf("delete run: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	return nil
}
