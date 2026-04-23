package database

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// Job is a unified row representing either a single benchmark_run or a
// test_suite_run. The Type field discriminates. PRD-36 introduced this so
// the Runs page has one feed, one offset, and one sort across both tables.
type Job struct {
	ID               string     `json:"id"`
	Type             string     `json:"type"` // "run" | "suite"
	ModelHfID        string     `json:"model_hf_id"`
	InstanceTypeName string     `json:"instance_type_name"`
	// FrameworkOrSuite holds the vLLM framework string for single runs,
	// and the suite_id for suite runs.
	FrameworkOrSuite string     `json:"framework_or_suite"`
	Status           string     `json:"status"`
	ErrorMessage     *string    `json:"error_message,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
	StartedAt        *time.Time `json:"started_at,omitempty"`
	CompletedAt      *time.Time `json:"completed_at,omitempty"`
}

// JobFilter is the superset of RunFilter and SuiteRun listing options. The
// Type filter lets the UI constrain to single runs or suites only; an empty
// Type returns both.
type JobFilter struct {
	Type    string // "" | "run" | "suite"
	Status  string // "" | "pending" | "running" | "completed" | "failed"
	Model   string // ILIKE substring against model_hf_id
	Sort    string // one of jobsAllowedSortColumns (default: "created_at")
	Order   string // "asc" | "desc" (default: "desc")
	Limit   int
	Offset  int
}

// jobsAllowedSortColumns maps user-facing sort keys to SQL expressions. Any
// column not present here falls back to the default (created_at DESC).
var jobsAllowedSortColumns = map[string]string{
	"created_at": "created_at",
	"status":     "status",
	"model":      "model_hf_id",
	"instance":   "instance_type_name",
	// Duration-based sort. NULL (in-flight) is pushed to the end of the
	// result regardless of direction via NULLS LAST.
	"duration": "(completed_at - started_at)",
}

// ListJobs returns a page of the unified job feed plus the total count of
// matching rows (for a "showing X-Y of Z" indicator). Implemented as a
// UNION over benchmark_runs + test_suite_runs with a synthetic `type`
// column so the page cursor and sort order are applied to the combined
// set, not each table independently.
func (r *Repository) ListJobs(ctx context.Context, f JobFilter) ([]Job, int, error) {
	var (
		conditions []string
		args       []any
		argIdx     int
	)

	if f.Type == "run" || f.Type == "suite" {
		argIdx++
		conditions = append(conditions, fmt.Sprintf("type = $%d", argIdx))
		args = append(args, f.Type)
	}
	if f.Status != "" {
		argIdx++
		conditions = append(conditions, fmt.Sprintf("status = $%d", argIdx))
		args = append(args, f.Status)
	}
	if f.Model != "" {
		argIdx++
		conditions = append(conditions, fmt.Sprintf("model_hf_id ILIKE $%d", argIdx))
		args = append(args, "%"+f.Model+"%")
	}

	where := ""
	if len(conditions) > 0 {
		where = "WHERE " + strings.Join(conditions, " AND ")
	}

	sortCol, ok := jobsAllowedSortColumns[f.Sort]
	if !ok {
		sortCol = "created_at"
	}
	dir := "DESC"
	if strings.EqualFold(f.Order, "asc") {
		dir = "ASC"
	}

	// Pagination bounds. Default 25 to match the PRD-36 shared page size;
	// cap at 200 to keep abusive requests honest.
	limit := 25
	if f.Limit > 0 && f.Limit <= 200 {
		limit = f.Limit
	}
	argIdx++
	limitArg := argIdx
	args = append(args, limit)

	argIdx++
	offsetArg := argIdx
	args = append(args, f.Offset)

	query := fmt.Sprintf(`
		WITH jobs AS (
			SELECT
				'run'::text                     AS type,
				br.id                           AS id,
				m.hf_id                         AS model_hf_id,
				it.name                         AS instance_type_name,
				br.framework                    AS framework_or_suite,
				br.status                       AS status,
				br.error_message                AS error_message,
				br.created_at                   AS created_at,
				br.started_at                   AS started_at,
				br.completed_at                 AS completed_at
			FROM benchmark_runs br
			JOIN models         m  ON br.model_id         = m.id
			JOIN instance_types it ON br.instance_type_id = it.id

			UNION ALL

			SELECT
				'suite'::text                   AS type,
				tsr.id                          AS id,
				m.hf_id                         AS model_hf_id,
				it.name                         AS instance_type_name,
				tsr.suite_id                    AS framework_or_suite,
				tsr.status                      AS status,
				NULL::text                      AS error_message,
				tsr.created_at                  AS created_at,
				tsr.started_at                  AS started_at,
				tsr.completed_at                AS completed_at
			FROM test_suite_runs tsr
			JOIN models         m  ON tsr.model_id         = m.id
			JOIN instance_types it ON tsr.instance_type_id = it.id
		)
		SELECT id, type, model_hf_id, instance_type_name, framework_or_suite,
		       status, error_message, created_at, started_at, completed_at,
		       COUNT(*) OVER () AS total_count
		FROM jobs
		%s
		ORDER BY %s %s NULLS LAST, created_at DESC
		LIMIT $%d OFFSET $%d
	`, where, sortCol, dir, limitArg, offsetArg)

	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("query jobs: %w", err)
	}
	defer rows.Close()

	var (
		items []Job
		total int
	)
	for rows.Next() {
		var j Job
		if err := rows.Scan(
			&j.ID, &j.Type, &j.ModelHfID, &j.InstanceTypeName, &j.FrameworkOrSuite,
			&j.Status, &j.ErrorMessage, &j.CreatedAt, &j.StartedAt, &j.CompletedAt,
			&total,
		); err != nil {
			return nil, 0, fmt.Errorf("scan job row: %w", err)
		}
		items = append(items, j)
	}
	return items, total, rows.Err()
}
