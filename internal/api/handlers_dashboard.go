package api

import (
	"net/http"
)

// handleDashboardStats serves GET /api/v1/dashboard/stats (PRD-35).
// Drives every stat card on the Dashboard page from server-side aggregates
// so the numbers reflect lifetime totals regardless of any pagination done
// on the list endpoints. `success_rate` is computed in Go because the SQL
// `0/0` case is a pain and this makes the semantics obvious.
func (s *Server) handleDashboardStats(w http.ResponseWriter, r *http.Request) {
	stats, err := s.repo.DashboardStats(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "dashboard stats: "+err.Error())
		return
	}

	var successRate float64
	denom := stats.CompletedCount + stats.FailedCount
	if denom > 0 {
		successRate = float64(stats.CompletedCount) / float64(denom) * 100
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"total_runs":      stats.TotalRuns,
		"total_single":    stats.TotalSingle,
		"total_suites":    stats.TotalSuites,
		"active_count":    stats.ActiveCount,
		"completed_count": stats.CompletedCount,
		"failed_count":    stats.FailedCount,
		"success_rate":    successRate,
		"cached_models":   stats.CachedModels,
		"total_cost_usd":  stats.TotalCostUSD,
		"cost_per_day":    stats.CostPerDay,
	})
}
