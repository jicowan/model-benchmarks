package database

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// ModelCacheFilter controls pagination and sort for ListModelCache (PRD-36).
// Autocomplete callers pass the zero value and get every row.
type ModelCacheFilter struct {
	Status string // ""|"pending"|"downloading"|"cached"|"failed" etc.
	Sort   string // see modelCacheAllowedSortColumns
	Order  string // "asc" | "desc" (default desc)
	Limit  int    // 0 = unbounded (autocomplete path)
	Offset int
}

var modelCacheAllowedSortColumns = map[string]string{
	"created_at": "created_at",
	"hf_id":      "hf_id",
	"size_bytes": "size_bytes",
	"status":     "status",
	"cached_at":  "cached_at",
}

func (r *Repository) CreateModelCache(ctx context.Context, m *ModelCache) (string, error) {
	var id string
	err := r.pool.QueryRow(ctx,
		`INSERT INTO model_cache (hf_id, hf_revision, s3_uri, display_name, status)
		 VALUES ($1, $2, $3, $4, $5)
		 RETURNING id`,
		m.HfID, m.HfRevision, m.S3URI, m.DisplayName, m.Status,
	).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("insert model cache: %w", err)
	}
	return id, nil
}

func (r *Repository) GetModelCache(ctx context.Context, id string) (*ModelCache, error) {
	var m ModelCache
	err := r.pool.QueryRow(ctx,
		`SELECT id, hf_id, hf_revision, s3_uri, display_name, size_bytes,
		        status, error_message, job_name, cached_at, created_at
		 FROM model_cache WHERE id = $1`, id,
	).Scan(&m.ID, &m.HfID, &m.HfRevision, &m.S3URI, &m.DisplayName, &m.SizeBytes,
		&m.Status, &m.ErrorMessage, &m.JobName, &m.CachedAt, &m.CreatedAt)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("query model cache: %w", err)
	}
	return &m, nil
}

func (r *Repository) GetModelCacheByHfID(ctx context.Context, hfID, revision string) (*ModelCache, error) {
	var m ModelCache
	err := r.pool.QueryRow(ctx,
		`SELECT id, hf_id, hf_revision, s3_uri, display_name, size_bytes,
		        status, error_message, job_name, cached_at, created_at
		 FROM model_cache WHERE hf_id = $1 AND hf_revision = $2`, hfID, revision,
	).Scan(&m.ID, &m.HfID, &m.HfRevision, &m.S3URI, &m.DisplayName, &m.SizeBytes,
		&m.Status, &m.ErrorMessage, &m.JobName, &m.CachedAt, &m.CreatedAt)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("query model cache by hf_id: %w", err)
	}
	return &m, nil
}

// ListModelCache returns a page of model_cache rows plus the total count of
// matching rows. When Limit=0 (the autocomplete path) the LIMIT clause is
// omitted and every row is returned.
func (r *Repository) ListModelCache(ctx context.Context, f ModelCacheFilter) ([]ModelCache, int, error) {
	var (
		conditions []string
		args       []any
		argIdx     int
	)

	if f.Status != "" {
		argIdx++
		conditions = append(conditions, fmt.Sprintf("status = $%d", argIdx))
		args = append(args, f.Status)
	}

	where := ""
	if len(conditions) > 0 {
		where = "WHERE " + strings.Join(conditions, " AND ")
	}

	sortCol, ok := modelCacheAllowedSortColumns[f.Sort]
	if !ok {
		sortCol = "created_at"
	}
	dir := "DESC"
	if strings.EqualFold(f.Order, "asc") {
		dir = "ASC"
	}

	limitClause := ""
	offsetClause := ""
	if f.Limit > 0 {
		argIdx++
		limitClause = fmt.Sprintf("LIMIT $%d", argIdx)
		args = append(args, f.Limit)

		argIdx++
		offsetClause = fmt.Sprintf("OFFSET $%d", argIdx)
		args = append(args, f.Offset)
	}

	query := fmt.Sprintf(`
		SELECT id, hf_id, hf_revision, s3_uri, display_name, size_bytes,
		       status, error_message, job_name, cached_at, created_at,
		       COUNT(*) OVER () AS total_count
		FROM model_cache
		%s
		ORDER BY %s %s NULLS LAST, created_at DESC
		%s %s
	`, where, sortCol, dir, limitClause, offsetClause)

	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("list model cache: %w", err)
	}
	defer rows.Close()

	var (
		items []ModelCache
		total int
	)
	for rows.Next() {
		var m ModelCache
		if err := rows.Scan(&m.ID, &m.HfID, &m.HfRevision, &m.S3URI, &m.DisplayName, &m.SizeBytes,
			&m.Status, &m.ErrorMessage, &m.JobName, &m.CachedAt, &m.CreatedAt, &total); err != nil {
			return nil, 0, fmt.Errorf("scan model cache row: %w", err)
		}
		items = append(items, m)
	}
	return items, total, rows.Err()
}

func (r *Repository) UpdateModelCacheStatus(ctx context.Context, id, status string, errMsg *string) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE model_cache SET status = $1, error_message = $2, job_name = COALESCE(job_name, job_name) WHERE id = $3`,
		status, errMsg, id)
	if err != nil {
		return fmt.Errorf("update model cache status: %w", err)
	}
	return nil
}

func (r *Repository) UpdateModelCacheComplete(ctx context.Context, id string, sizeBytes int64) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE model_cache SET status = 'cached', size_bytes = $1, cached_at = $2 WHERE id = $3`,
		sizeBytes, time.Now(), id)
	if err != nil {
		return fmt.Errorf("update model cache complete: %w", err)
	}
	return nil
}

func (r *Repository) DeleteModelCache(ctx context.Context, id string) error {
	_, err := r.pool.Exec(ctx, `DELETE FROM model_cache WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("delete model cache: %w", err)
	}
	return nil
}
