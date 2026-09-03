package main

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/accelbench/accelbench/internal/api"
	"github.com/accelbench/accelbench/internal/database"
)

// PRD-68 P3/P6: operational endpoints.
//
//   /healthz  liveness — process is up. Deliberately DB-independent so a
//             transient Aurora blip never restarts pods that own runs.
//   /readyz   readiness — 200 only when the DB answers a ping within 2s and
//             the pod is not draining. Endpoints stop routing to a draining
//             pod within a probe period, which is what lets the graceful
//             shutdown finish in-flight runs without new HTTP traffic.
//   /metrics  Prometheus text exposition of the orchestrator gauges. Hand
//             rolled (a dozen lines) rather than pulling in client_golang.

func registerOpsRoutes(mux *http.ServeMux, srv *api.Server, repo database.Repo) {
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, "ok")
	})

	mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		if srv.Orchestrator().Draining() {
			http.Error(w, "draining", http.StatusServiceUnavailable)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := repo.Ping(ctx); err != nil {
			http.Error(w, "database unreachable", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, "ready")
	})

	mux.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		st := srv.Orchestrator().Stats()
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		gauge := func(name, help string, v int64) {
			fmt.Fprintf(w, "# HELP %s %s\n# TYPE %s gauge\n%s %d\n", name, help, name, name, v)
		}
		counter := func(name, help string, v int64) {
			fmt.Fprintf(w, "# HELP %s %s\n# TYPE %s counter\n%s %d\n", name, help, name, name, v)
		}
		b2i := func(b bool) int64 {
			if b {
				return 1
			}
			return 0
		}
		gauge("accelbench_runs_active", "Orchestrations holding a concurrency slot on this pod.", st.ActiveRuns)
		gauge("accelbench_runs_queued", "Orchestrations waiting for a concurrency slot on this pod.", st.QueuedRuns)
		gauge("accelbench_runs_max_concurrent", "Concurrency slots configured on this pod (MAX_CONCURRENT_RUNS).", st.MaxConcurrent)
		gauge("accelbench_pod_draining", "1 while this pod is refusing new work and finishing in-flight runs.", b2i(st.Draining))
		counter("accelbench_runs_admitted_total", "Orchestrations that acquired a slot since process start.", st.Admitted)
		counter("accelbench_runs_rejected_draining_total", "Orchestrations refused because the pod was draining.", st.RejectedDraining)
		counter("accelbench_recovery_actions_total", "Orphaned runs/suites salvaged or failed by this pod's recovery loop.", st.Recovered)
		counter("accelbench_runs_fenced_total", "Runs cancelled locally because ownership moved to another pod.", st.Fenced)
		counter("accelbench_coordination_poll_errors_total", "Shared cancel/ownership poll passes that failed on the DB.", st.PollErrors)
	})
}
