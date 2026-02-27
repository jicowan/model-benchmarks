package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/accelbench/accelbench/internal/database"
)

func TestListCatalog(t *testing.T) {
	entries := []database.CatalogEntry{
		{
			RunID:            "run-1",
			ModelHfID:        "meta-llama/Llama-3.1-70B",
			InstanceTypeName: "p5.48xlarge",
			AcceleratorName:  "H100",
		},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/catalog" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.URL.Query().Get("model") != "meta-llama/Llama-3.1-70B" {
			t.Errorf("expected model filter, got query: %s", r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(entries)
	}))
	defer srv.Close()

	c := New(srv.URL)
	result, err := c.ListCatalog(context.Background(), database.CatalogFilter{
		ModelHfID: "meta-llama/Llama-3.1-70B",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(result))
	}
	if result[0].RunID != "run-1" {
		t.Errorf("unexpected run ID: %s", result[0].RunID)
	}
}

func TestListCatalog_AllFilters(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("model_family") != "llama" {
			t.Errorf("expected model_family=llama, got %s", q.Get("model_family"))
		}
		if q.Get("instance_family") != "p5" {
			t.Errorf("expected instance_family=p5, got %s", q.Get("instance_family"))
		}
		if q.Get("accelerator_type") != "gpu" {
			t.Errorf("expected accelerator_type=gpu, got %s", q.Get("accelerator_type"))
		}
		if q.Get("sort") != "throughput_aggregate" {
			t.Errorf("expected sort=throughput_aggregate, got %s", q.Get("sort"))
		}
		if q.Get("order") != "desc" {
			t.Errorf("expected order=desc, got %s", q.Get("order"))
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]database.CatalogEntry{})
	}))
	defer srv.Close()

	c := New(srv.URL)
	_, err := c.ListCatalog(context.Background(), database.CatalogFilter{
		ModelFamily:     "llama",
		InstanceFamily:  "p5",
		AcceleratorType: "gpu",
		SortBy:          "throughput_aggregate",
		SortDesc:        true,
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestCreateRun(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/api/v1/runs" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		var req database.RunRequest
		json.NewDecoder(r.Body).Decode(&req)
		if req.ModelHfID != "test-model" {
			t.Errorf("unexpected model: %s", req.ModelHfID)
		}
		w.WriteHeader(http.StatusAccepted)
		json.NewEncoder(w).Encode(map[string]string{"id": "run-123", "status": "pending"})
	}))
	defer srv.Close()

	c := New(srv.URL)
	id, status, err := c.CreateRun(context.Background(), database.RunRequest{
		ModelHfID:    "test-model",
		RunType:      "on_demand",
		Concurrency:  1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if id != "run-123" {
		t.Errorf("expected run-123, got %s", id)
	}
	if status != "pending" {
		t.Errorf("expected pending, got %s", status)
	}
}

func TestGetRun(t *testing.T) {
	run := database.BenchmarkRun{
		ID:     "run-abc",
		Status: "completed",
		CreatedAt: time.Now(),
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/runs/run-abc" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		json.NewEncoder(w).Encode(run)
	}))
	defer srv.Close()

	c := New(srv.URL)
	result, err := c.GetRun(context.Background(), "run-abc")
	if err != nil {
		t.Fatal(err)
	}
	if result.ID != "run-abc" {
		t.Errorf("expected run-abc, got %s", result.ID)
	}
	if result.Status != "completed" {
		t.Errorf("expected completed, got %s", result.Status)
	}
}

func TestGetMetrics(t *testing.T) {
	ttft := 15.5
	tput := 1200.0
	metrics := database.BenchmarkMetrics{
		ID:    "m-1",
		RunID: "run-xyz",
		TTFTP50Ms: &ttft,
		ThroughputAggregateTPS: &tput,
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/runs/run-xyz/metrics" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		json.NewEncoder(w).Encode(metrics)
	}))
	defer srv.Close()

	c := New(srv.URL)
	result, err := c.GetMetrics(context.Background(), "run-xyz")
	if err != nil {
		t.Fatal(err)
	}
	if result.RunID != "run-xyz" {
		t.Errorf("expected run-xyz, got %s", result.RunID)
	}
	if result.TTFTP50Ms == nil || *result.TTFTP50Ms != 15.5 {
		t.Error("unexpected TTFT p50")
	}
}

func TestAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": "run not found"})
	}))
	defer srv.Close()

	c := New(srv.URL)
	_, err := c.GetRun(context.Background(), "nonexistent")
	if err == nil {
		t.Fatal("expected error")
	}
	if got := err.Error(); got != "API error 404: run not found" {
		t.Errorf("unexpected error message: %s", got)
	}
}

func TestCreateRun_APIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": "model not found"})
	}))
	defer srv.Close()

	c := New(srv.URL)
	_, _, err := c.CreateRun(context.Background(), database.RunRequest{ModelHfID: "missing"})
	if err == nil {
		t.Fatal("expected error")
	}
	if got := err.Error(); got != "API error 404: model not found" {
		t.Errorf("unexpected error: %s", got)
	}
}
