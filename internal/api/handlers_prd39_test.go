package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/accelbench/accelbench/internal/cache"
	"github.com/accelbench/accelbench/internal/database"

	"k8s.io/client-go/kubernetes/fake"
)

func setupPRD39Server() (*Server, *http.ServeMux, string) {
	repo := seedRepo()
	client := fake.NewSimpleClientset()
	srv := NewServer(repo, client, "test-pod")
	srv.SetCache(cache.New(60 * time.Second))
	mux := http.NewServeMux()
	srv.RegisterRoutes(mux)

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

	ttft := 25.0
	srv.repo.PersistMetrics(ctx, id, &database.BenchmarkMetrics{TTFTP50Ms: &ttft})

	region := os.Getenv("AWS_REGION")
	if region == "" {
		region = "us-east-2"
	}
	srv.repo.UpsertPricing(ctx, &database.Pricing{
		InstanceTypeID:    "inst-001",
		Region:            region,
		OnDemandHourlyUSD: 1.23,
		EffectiveDate:     "2026-01-01",
	})

	return srv, mux, id
}

// ---------- Bare compatibility ----------

func TestRunDetail_BareResponse_NoIncludeFields(t *testing.T) {
	_, mux, id := setupPRD39Server()

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/runs/"+id, nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var raw map[string]any
	json.NewDecoder(w.Body).Decode(&raw)

	for _, key := range []string{"metrics", "instance", "pricing", "oom", "errors"} {
		if _, ok := raw[key]; ok {
			t.Errorf("bare response should not contain %q", key)
		}
	}
	if raw["id"] == nil {
		t.Error("bare response missing id")
	}
}

func TestRunDetail_BareResponse_HasETag(t *testing.T) {
	_, mux, id := setupPRD39Server()

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/runs/"+id, nil))
	if w.Header().Get("ETag") == "" {
		t.Error("bare completed run should have ETag")
	}
}

// ---------- Single include ----------

func TestRunDetail_IncludeMetrics(t *testing.T) {
	_, mux, id := setupPRD39Server()

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/runs/"+id+"?include=metrics", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}

	var raw map[string]any
	json.NewDecoder(w.Body).Decode(&raw)

	if raw["metrics"] == nil {
		t.Error("expected metrics in response")
	}
	if raw["instance"] != nil {
		t.Error("should not contain instance without requesting it")
	}
	if raw["pricing"] != nil {
		t.Error("should not contain pricing without requesting it")
	}
}

// ---------- Multiple includes ----------

func TestRunDetail_IncludeMultiple(t *testing.T) {
	_, mux, id := setupPRD39Server()

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/runs/"+id+"?include=metrics,instance,pricing", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}

	var raw map[string]any
	json.NewDecoder(w.Body).Decode(&raw)

	for _, key := range []string{"metrics", "instance", "pricing"} {
		if raw[key] == nil {
			t.Errorf("expected %q in response", key)
		}
	}
}

// ---------- Unknown token silently ignored ----------

func TestRunDetail_UnknownTokenIgnored(t *testing.T) {
	_, mux, id := setupPRD39Server()

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/runs/"+id+"?include=bogus", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}

	var raw map[string]any
	json.NewDecoder(w.Body).Decode(&raw)

	if raw["errors"] != nil {
		t.Error("unknown token should not produce errors")
	}
	if raw["id"] == nil {
		t.Error("base response missing id")
	}
}

// ---------- ETag skipped with includes ----------

func TestRunDetail_NoETagWithIncludes(t *testing.T) {
	_, mux, id := setupPRD39Server()

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/runs/"+id+"?include=metrics", nil))
	if w.Header().Get("ETag") != "" {
		t.Error("should not have ETag when ?include= is set")
	}
}

// ---------- Pricing include returns PricingRow shape ----------

func TestRunDetail_PricingIncludeShape(t *testing.T) {
	_, mux, id := setupPRD39Server()

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/runs/"+id+"?include=pricing", nil))

	var raw map[string]any
	json.NewDecoder(w.Body).Decode(&raw)

	pr, ok := raw["pricing"].(map[string]any)
	if !ok {
		t.Fatalf("pricing should be an object, got %v (errors: %v)", raw["pricing"], raw["errors"])
		return
	}
	if pr["instance_type_name"] == nil {
		t.Error("pricing should have instance_type_name")
	}
	if pr["on_demand_hourly_usd"] == nil {
		t.Error("pricing should have on_demand_hourly_usd")
	}
}

// ---------- Suite includes ----------

func TestSuiteDetail_IncludeInstance(t *testing.T) {
	srv, mux, _ := setupPRD39Server()

	ctx := context.Background()
	suiteRun := &database.TestSuiteRun{
		ModelID: "model-001", InstanceTypeID: "inst-001",
		SuiteID:              "standard-load-test",
		TensorParallelDegree: 1,
		Status:               "pending",
	}
	suiteID, _ := srv.repo.CreateTestSuiteRun(ctx, suiteRun)
	srv.repo.UpdateSuiteRunStatus(ctx, suiteID, "completed", nil)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/suite-runs/"+suiteID+"?include=instance,pricing", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; body: %s", w.Code, w.Body.String())
	}

	var raw map[string]any
	json.NewDecoder(w.Body).Decode(&raw)

	if raw["instance"] == nil {
		t.Error("expected instance in suite response")
	}
	if raw["pricing"] == nil {
		t.Error("expected pricing in suite response")
	}
}

func TestSuiteDetail_BareResponse_NoIncludeFields(t *testing.T) {
	srv, mux, _ := setupPRD39Server()

	ctx := context.Background()
	suiteRun := &database.TestSuiteRun{
		ModelID: "model-001", InstanceTypeID: "inst-001",
		SuiteID:              "standard-load-test",
		TensorParallelDegree: 1,
		Status:               "pending",
	}
	suiteID, _ := srv.repo.CreateTestSuiteRun(ctx, suiteRun)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/suite-runs/"+suiteID, nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}

	var raw map[string]any
	json.NewDecoder(w.Body).Decode(&raw)

	for _, key := range []string{"instance", "pricing", "errors"} {
		if v, ok := raw[key]; ok && v != nil {
			t.Errorf("bare suite response should not contain %q", key)
		}
	}
}
