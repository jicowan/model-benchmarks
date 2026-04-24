package api

import (
	"context"
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/accelbench/accelbench/internal/cache"
	"github.com/accelbench/accelbench/internal/database"

	"k8s.io/client-go/kubernetes/fake"
)

func setupCachedServer() (*Server, *http.ServeMux) {
	repo := seedRepo()
	client := fake.NewSimpleClientset()
	srv := NewServer(repo, client, "test-pod")
	srv.SetCache(cache.New(60 * time.Second))
	mux := http.NewServeMux()
	srv.RegisterRoutes(mux)
	return srv, mux
}

// ---------- TTL cache HIT/MISS ----------

func TestCachedEndpoint_HitMiss(t *testing.T) {
	_, mux := setupCachedServer()

	// First call: MISS
	w1 := httptest.NewRecorder()
	mux.ServeHTTP(w1, httptest.NewRequest("GET", "/api/v1/instance-types", nil))
	if w1.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w1.Code)
	}
	if w1.Header().Get("X-Cache") != "MISS" {
		t.Errorf("first call X-Cache = %q, want MISS", w1.Header().Get("X-Cache"))
	}

	// Second call: HIT
	w2 := httptest.NewRecorder()
	mux.ServeHTTP(w2, httptest.NewRequest("GET", "/api/v1/instance-types", nil))
	if w2.Header().Get("X-Cache") != "HIT" {
		t.Errorf("second call X-Cache = %q, want HIT", w2.Header().Get("X-Cache"))
	}

	// Bodies must be identical.
	if w1.Body.String() != w2.Body.String() {
		t.Error("cached body differs from uncached body")
	}
}

func TestCachedEndpoint_Scenarios(t *testing.T) {
	_, mux := setupCachedServer()

	w1 := httptest.NewRecorder()
	mux.ServeHTTP(w1, httptest.NewRequest("GET", "/api/v1/scenarios", nil))
	if w1.Header().Get("X-Cache") != "MISS" {
		t.Errorf("X-Cache = %q, want MISS", w1.Header().Get("X-Cache"))
	}
	w2 := httptest.NewRecorder()
	mux.ServeHTTP(w2, httptest.NewRequest("GET", "/api/v1/scenarios", nil))
	if w2.Header().Get("X-Cache") != "HIT" {
		t.Errorf("X-Cache = %q, want HIT", w2.Header().Get("X-Cache"))
	}
}

func TestCachedEndpoint_DashboardStats(t *testing.T) {
	_, mux := setupCachedServer()

	w1 := httptest.NewRecorder()
	mux.ServeHTTP(w1, httptest.NewRequest("GET", "/api/v1/dashboard/stats", nil))
	if w1.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w1.Code)
	}
	if w1.Header().Get("X-Cache") != "MISS" {
		t.Errorf("X-Cache = %q, want MISS", w1.Header().Get("X-Cache"))
	}
	w2 := httptest.NewRecorder()
	mux.ServeHTTP(w2, httptest.NewRequest("GET", "/api/v1/dashboard/stats", nil))
	if w2.Header().Get("X-Cache") != "HIT" {
		t.Errorf("X-Cache = %q, want HIT", w2.Header().Get("X-Cache"))
	}
}

// ---------- No X-Cache on uncached endpoints ----------

func TestUncachedEndpoint_NoXCacheHeader(t *testing.T) {
	_, mux := setupCachedServer()

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/catalog?limit=5", nil))
	if w.Header().Get("X-Cache") != "" {
		t.Errorf("catalog should have no X-Cache header, got %q", w.Header().Get("X-Cache"))
	}
}

// ---------- NopCache produces MISS ----------

func TestNopCache_AlwaysMiss(t *testing.T) {
	_, mux := setupServer() // no SetCache → NopCache

	w1 := httptest.NewRecorder()
	mux.ServeHTTP(w1, httptest.NewRequest("GET", "/api/v1/instance-types", nil))
	if w1.Header().Get("X-Cache") != "MISS" {
		t.Errorf("NopCache first call X-Cache = %q, want MISS", w1.Header().Get("X-Cache"))
	}
	w2 := httptest.NewRecorder()
	mux.ServeHTTP(w2, httptest.NewRequest("GET", "/api/v1/instance-types", nil))
	if w2.Header().Get("X-Cache") != "MISS" {
		t.Errorf("NopCache second call X-Cache = %q, want MISS (always miss)", w2.Header().Get("X-Cache"))
	}
}

// ---------- ETag on completed runs ----------

func TestETag_CompletedRun_304(t *testing.T) {
	srv, mux := setupCachedServer()

	// Seed a completed run.
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

	// First GET — 200 with ETag.
	w1 := httptest.NewRecorder()
	mux.ServeHTTP(w1, httptest.NewRequest("GET", "/api/v1/runs/"+id, nil))
	if w1.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w1.Code)
	}
	etag := w1.Header().Get("ETag")
	if etag == "" {
		t.Fatal("expected ETag header on completed run")
	}
	if w1.Header().Get("Cache-Control") == "" {
		t.Error("expected Cache-Control header on completed run")
	}

	// Second GET with If-None-Match — 304.
	req2 := httptest.NewRequest("GET", "/api/v1/runs/"+id, nil)
	req2.Header.Set("If-None-Match", etag)
	w2 := httptest.NewRecorder()
	mux.ServeHTTP(w2, req2)
	if w2.Code != http.StatusNotModified {
		t.Errorf("status = %d, want 304", w2.Code)
	}
	if w2.Body.Len() > 0 {
		t.Error("304 response should have empty body")
	}
}

func TestETag_RunningRun_NoETag(t *testing.T) {
	srv, mux := setupCachedServer()

	ctx := context.Background()
	run := &database.BenchmarkRun{
		ModelID: "model-001", InstanceTypeID: "inst-001",
		Framework: "vllm", FrameworkVersion: "v0.6.0",
		TensorParallelDegree: 1, Concurrency: 16,
		InputSequenceLength: 512, OutputSequenceLength: 256,
		DatasetName: "sharegpt", RunType: "on_demand", Status: "pending",
	}
	id, _ := srv.repo.CreateBenchmarkRun(ctx, run)
	srv.repo.UpdateRunStatus(ctx, id, "running")

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/runs/"+id, nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if w.Header().Get("ETag") != "" {
		t.Error("running run should have no ETag header")
	}
}

func TestETag_FailedRun_HasETag(t *testing.T) {
	srv, mux := setupCachedServer()

	ctx := context.Background()
	run := &database.BenchmarkRun{
		ModelID: "model-001", InstanceTypeID: "inst-001",
		Framework: "vllm", FrameworkVersion: "v0.6.0",
		TensorParallelDegree: 1, Concurrency: 16,
		InputSequenceLength: 512, OutputSequenceLength: 256,
		DatasetName: "sharegpt", RunType: "on_demand", Status: "pending",
	}
	id, _ := srv.repo.CreateBenchmarkRun(ctx, run)
	srv.repo.UpdateRunStatus(ctx, id, "failed")

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/runs/"+id, nil))
	if w.Header().Get("ETag") == "" {
		t.Error("failed run should have ETag header")
	}
}

func TestETag_CompletedSuiteRun_304(t *testing.T) {
	srv, mux := setupCachedServer()

	ctx := context.Background()
	suiteRun := &database.TestSuiteRun{
		ModelID: "model-001", InstanceTypeID: "inst-001",
		SuiteID:              "standard-load-test",
		TensorParallelDegree: 1,
		Status:               "pending",
	}
	id, _ := srv.repo.CreateTestSuiteRun(ctx, suiteRun)
	srv.repo.UpdateSuiteRunStatus(ctx, id, "completed", nil)

	// First GET.
	w1 := httptest.NewRecorder()
	mux.ServeHTTP(w1, httptest.NewRequest("GET", "/api/v1/suite-runs/"+id, nil))
	if w1.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w1.Code, w1.Body.String())
	}
	etag := w1.Header().Get("ETag")
	if etag == "" {
		t.Fatal("expected ETag header on completed suite run")
	}

	// Second GET with If-None-Match.
	req2 := httptest.NewRequest("GET", "/api/v1/suite-runs/"+id, nil)
	req2.Header.Set("If-None-Match", etag)
	w2 := httptest.NewRecorder()
	mux.ServeHTTP(w2, req2)
	if w2.Code != http.StatusNotModified {
		t.Errorf("status = %d, want 304", w2.Code)
	}
}

// ---------- Invalidation ----------

func TestCacheInvalidation_ToolVersions(t *testing.T) {
	_, mux := setupCachedServer()

	// Prime the cache.
	w1 := httptest.NewRecorder()
	mux.ServeHTTP(w1, httptest.NewRequest("GET", "/api/v1/config/tool-versions", nil))
	if w1.Header().Get("X-Cache") != "MISS" {
		t.Fatalf("expected MISS on first call, got %q", w1.Header().Get("X-Cache"))
	}
	w2 := httptest.NewRecorder()
	mux.ServeHTTP(w2, httptest.NewRequest("GET", "/api/v1/config/tool-versions", nil))
	if w2.Header().Get("X-Cache") != "HIT" {
		t.Fatalf("expected HIT on second call, got %q", w2.Header().Get("X-Cache"))
	}

	// PUT invalidates.
	body, _ := json.Marshal(map[string]string{
		"framework_version":      "v0.7.0",
		"inference_perf_version": "v0.3.0",
	})
	wput := httptest.NewRecorder()
	req := httptest.NewRequest("PUT", "/api/v1/config/tool-versions", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(wput, req)
	if wput.Code != http.StatusOK {
		t.Fatalf("PUT status = %d, want 200; body: %s", wput.Code, wput.Body.String())
	}

	// After PUT, cache should be invalidated → MISS.
	w3 := httptest.NewRecorder()
	mux.ServeHTTP(w3, httptest.NewRequest("GET", "/api/v1/config/tool-versions", nil))
	if w3.Header().Get("X-Cache") != "MISS" {
		t.Errorf("expected MISS after invalidation, got %q", w3.Header().Get("X-Cache"))
	}
}
