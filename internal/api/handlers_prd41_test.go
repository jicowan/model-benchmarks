package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/accelbench/accelbench/internal/database"

	"k8s.io/client-go/kubernetes/fake"
)

func setupPRD41Server() (*Server, *http.ServeMux) {
	repo := seedRepo()
	client := fake.NewSimpleClientset()
	srv := NewServer(repo, client, "test-pod")
	mux := http.NewServeMux()
	srv.RegisterRoutes(mux)
	return srv, mux
}

func TestHandleExportRunCSV_Success(t *testing.T) {
	srv, mux := setupPRD41Server()

	ctx := context.Background()
	run := &database.BenchmarkRun{
		ModelID: "model-001", InstanceTypeID: "inst-001",
		Framework: "vllm", FrameworkVersion: "v0.6.0",
		TensorParallelDegree: 1, Concurrency: 16,
		InputSequenceLength: 512, OutputSequenceLength: 256,
		DatasetName: "sharegpt", RunType: "on_demand", Status: "pending",
	}
	id, _ := srv.repo.CreateBenchmarkRun(ctx, run)
	srv.repo.UpdateRunStatus(ctx, id, "completed")
	ttft := 42.0
	srv.repo.PersistMetrics(ctx, id, &database.BenchmarkMetrics{TTFTP50Ms: &ttft})

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/runs/"+id+"/csv", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/csv") {
		t.Errorf("content-type = %q, want text/csv", ct)
	}
	if cd := w.Header().Get("Content-Disposition"); !strings.Contains(cd, "attachment") || !strings.Contains(cd, ".csv") {
		t.Errorf("content-disposition = %q", cd)
	}
	body := w.Body.String()
	if !strings.Contains(body, "run_id,"+id) {
		t.Errorf("CSV missing run_id row; body:\n%s", body)
	}
	if !strings.Contains(body, "ttft_p50_ms,42.00") {
		t.Error("CSV missing TTFT metric")
	}
}

func TestHandleExportRunCSV_404(t *testing.T) {
	_, mux := setupPRD41Server()
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/runs/nonexistent/csv", nil))
	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", w.Code)
	}
}

func TestHandleExportSuiteCSV_Success(t *testing.T) {
	srv, mux := setupPRD41Server()

	ctx := context.Background()
	suiteRun := &database.TestSuiteRun{
		ModelID: "model-001", InstanceTypeID: "inst-001",
		SuiteID:              "standard-load-test",
		TensorParallelDegree: 1,
		Status:               "pending",
	}
	id, _ := srv.repo.CreateTestSuiteRun(ctx, suiteRun)
	srv.repo.UpdateSuiteRunStatus(ctx, id, "completed", nil)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/suite-runs/"+id+"/csv", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/csv") {
		t.Errorf("content-type = %q, want text/csv", ct)
	}
	body := w.Body.String()
	if !strings.Contains(body, "# suite_run_id: "+id) {
		t.Errorf("suite CSV missing suite_run_id comment; body:\n%s", body)
	}
	if !strings.Contains(body, "scenario_id,scenario_name") {
		t.Error("suite CSV missing column header")
	}
}

func TestHandleExportSuiteManifest_Success(t *testing.T) {
	srv, mux := setupPRD41Server()

	ctx := context.Background()
	fw := "vllm"
	fv := "v0.6.0"
	suiteRun := &database.TestSuiteRun{
		ModelID: "model-001", InstanceTypeID: "inst-001",
		SuiteID:              "standard-load-test",
		TensorParallelDegree: 1,
		Status:               "pending",
		Framework:            &fw,
		FrameworkVersion:     &fv,
	}
	id, _ := srv.repo.CreateTestSuiteRun(ctx, suiteRun)
	srv.repo.UpdateSuiteRunStatus(ctx, id, "completed", nil)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/suite-runs/"+id+"/export", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "yaml") {
		t.Errorf("content-type = %q, want yaml", ct)
	}
	body := w.Body.String()
	if !strings.Contains(body, "meta-llama/Llama-3.1-8B") {
		t.Error("manifest missing model HF ID")
	}
	if !strings.Contains(body, "apiVersion: apps/v1") {
		t.Error("manifest missing K8s Deployment preamble")
	}
}

func TestHandleExportSuiteManifest_RejectsInFlight(t *testing.T) {
	srv, mux := setupPRD41Server()

	ctx := context.Background()
	suiteRun := &database.TestSuiteRun{
		ModelID: "model-001", InstanceTypeID: "inst-001",
		SuiteID:              "standard-load-test",
		TensorParallelDegree: 1,
		Status:               "pending",
	}
	id, _ := srv.repo.CreateTestSuiteRun(ctx, suiteRun)
	srv.repo.UpdateSuiteRunStatus(ctx, id, "running", nil)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/suite-runs/"+id+"/export", nil))

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for in-flight suite", w.Code)
	}
}

func TestOldHTMLExportEndpoints_404(t *testing.T) {
	_, mux := setupPRD41Server()

	for _, path := range []string{
		"/api/v1/runs/any-id/report",
		"/api/v1/compare/report?ids=a,b",
	} {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != http.StatusNotFound {
			t.Errorf("%s: status = %d, want 404 (handler removed)", path, w.Code)
		}
	}
}
