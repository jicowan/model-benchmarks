package api

import (
	"context"
	"net/http"
	"time"
)

// StatusResponse describes the health of each subsystem the UI cares about.
// This endpoint is meant to be called by the frontend — it is NOT a K8s
// liveness/readiness probe. Taking a dependency on the DB from those probes
// would cause pod restarts on transient DB issues, which doesn't improve
// availability.
type StatusResponse struct {
	Status     string                     `json:"status"` // "ok" | "degraded" | "down"
	Components map[string]ComponentStatus `json:"components"`
	CheckedAt  string                     `json:"checked_at"`
}

// ComponentStatus holds the state of a single subsystem.
type ComponentStatus struct {
	Status     string `json:"status"` // "ok" | "down"
	LatencyMs  int64  `json:"latency_ms,omitempty"`
	Error      string `json:"error,omitempty"`
}

// handleStatus reports API + DB health for the UI. The API itself is "ok"
// if the request was served (i.e. the process is alive). The DB component
// is probed with a Ping that has a short timeout so a stuck DB can't stall
// the UI.
func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	components := map[string]ComponentStatus{
		"api": {Status: "ok"},
	}

	// Probe DB with a tight timeout so the UI gets a fast response.
	dbCtx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	dbStart := time.Now()
	if err := s.repo.Ping(dbCtx); err != nil {
		components["database"] = ComponentStatus{
			Status: "down",
			Error:  err.Error(),
		}
	} else {
		components["database"] = ComponentStatus{
			Status:    "ok",
			LatencyMs: time.Since(dbStart).Milliseconds(),
		}
	}

	overall := "ok"
	for _, c := range components {
		if c.Status != "ok" {
			overall = "degraded"
		}
	}

	resp := StatusResponse{
		Status:     overall,
		Components: components,
		CheckedAt:  time.Now().UTC().Format(time.RFC3339),
	}

	// Return 200 even when degraded so the UI can show component detail;
	// the client inspects `status` to decide what to render.
	writeJSON(w, http.StatusOK, resp)
}
