package database

import (
	"context"
	"fmt"
	"time"
)

// DashboardStats is the full response for GET /api/v1/dashboard/stats (PRD-35).
// Every field is computed server-side from live tables so the Dashboard
// cards never undercount (the previous client-side aggregation capped at
// whatever page size the Dashboard fetched).
type DashboardStats struct {
	TotalRuns      int       `json:"total_runs"`      // benchmark_runs + test_suite_runs
	TotalSingle    int       `json:"total_single"`    // just benchmark_runs
	TotalSuites    int       `json:"total_suites"`    // just test_suite_runs
	ActiveCount    int       `json:"active_count"`    // pending + running across both tables
	CompletedCount int       `json:"completed_count"` // completed across both tables
	FailedCount    int       `json:"failed_count"`    // failed across both tables
	CachedModels   int       `json:"cached_models"`   // model_cache rows with status='cached'
	TotalCostUSD   float64   `json:"total_cost_usd"`  // SUM over both tables, NULLs COALESCE to 0
	CostPerDay     []DayCost `json:"cost_per_day"`    // last 14 UTC days, zero-filled
}

// DayCost is one bucket of the 14-day cost series.
type DayCost struct {
	Day     string  `json:"day"`      // "YYYY-MM-DD"
	CostUSD float64 `json:"cost_usd"` // 0.0 on days with no cost-stamped runs
}

// DashboardStats runs three aggregate queries: union-count per status,
// SUM(total_cost_usd) across both run tables, and a 14-day cost series
// zero-filled via generate_series. No JOINs — keeps the query cheap.
func (r *Repository) DashboardStats(ctx context.Context) (*DashboardStats, error) {
	stats := &DashboardStats{}

	// Per-status counts, split by source table so we can expose total_single
	// and total_suites separately on the same round-trip.
	err := r.pool.QueryRow(ctx, `
		WITH combined AS (
			SELECT 'run'   AS kind, status FROM benchmark_runs
			UNION ALL
			SELECT 'suite' AS kind, status FROM test_suite_runs
		)
		SELECT
			COUNT(*)                                                   AS total_runs,
			COUNT(*) FILTER (WHERE kind = 'run')                       AS total_single,
			COUNT(*) FILTER (WHERE kind = 'suite')                     AS total_suites,
			COUNT(*) FILTER (WHERE status IN ('pending','running'))    AS active_count,
			COUNT(*) FILTER (WHERE status = 'completed')               AS completed_count,
			COUNT(*) FILTER (WHERE status = 'failed')                  AS failed_count
		FROM combined
	`).Scan(&stats.TotalRuns, &stats.TotalSingle, &stats.TotalSuites,
		&stats.ActiveCount, &stats.CompletedCount, &stats.FailedCount)
	if err != nil {
		return nil, fmt.Errorf("dashboard status counts: %w", err)
	}

	// Cached model count is independent of run stats.
	err = r.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM model_cache WHERE status = 'cached'`).
		Scan(&stats.CachedModels)
	if err != nil {
		return nil, fmt.Errorf("dashboard cached models: %w", err)
	}

	// Lifetime cost — benchmark_runs + test_suite_runs both hold their own
	// total_cost_usd (suite cost isn't a sum of children because scenarios
	// aren't stored as benchmark_runs).
	err = r.pool.QueryRow(ctx, `
		SELECT
			COALESCE((SELECT SUM(total_cost_usd) FROM benchmark_runs),  0) +
			COALESCE((SELECT SUM(total_cost_usd) FROM test_suite_runs), 0)
	`).Scan(&stats.TotalCostUSD)
	if err != nil {
		return nil, fmt.Errorf("dashboard cost sum: %w", err)
	}

	// Per-day cost for the last 14 days, zero-filled. generate_series builds
	// the calendar; LEFT JOIN ensures days with no runs produce a 0.00 row.
	rows, err := r.pool.Query(ctx, `
		WITH days AS (
			SELECT (CURRENT_DATE - INTERVAL '13 days' + (n || ' days')::INTERVAL)::date AS day
			FROM generate_series(0, 13) AS g(n)
		),
		run_costs AS (
			SELECT DATE(created_at) AS day, total_cost_usd
			FROM benchmark_runs WHERE total_cost_usd IS NOT NULL
			UNION ALL
			SELECT DATE(created_at) AS day, total_cost_usd
			FROM test_suite_runs WHERE total_cost_usd IS NOT NULL
		)
		SELECT d.day::text, COALESCE(SUM(rc.total_cost_usd), 0)::float8
		FROM days d
		LEFT JOIN run_costs rc ON rc.day = d.day
		GROUP BY d.day
		ORDER BY d.day ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("dashboard cost per day: %w", err)
	}
	defer rows.Close()

	stats.CostPerDay = make([]DayCost, 0, 14)
	for rows.Next() {
		var dc DayCost
		if err := rows.Scan(&dc.Day, &dc.CostUSD); err != nil {
			return nil, fmt.Errorf("scan day cost: %w", err)
		}
		stats.CostPerDay = append(stats.CostPerDay, dc)
	}

	// Defensive: if generate_series + time zone drift ever produces fewer
	// than 14 rows, pad the front. Normally a no-op.
	if len(stats.CostPerDay) < 14 {
		today := time.Now().UTC().Truncate(24 * time.Hour)
		backfill := make([]DayCost, 0, 14)
		for i := 13; i >= 0; i-- {
			d := today.AddDate(0, 0, -i).Format("2006-01-02")
			var existing *DayCost
			for j := range stats.CostPerDay {
				if stats.CostPerDay[j].Day == d {
					existing = &stats.CostPerDay[j]
					break
				}
			}
			if existing != nil {
				backfill = append(backfill, *existing)
			} else {
				backfill = append(backfill, DayCost{Day: d})
			}
		}
		stats.CostPerDay = backfill
	}

	return stats, nil
}
