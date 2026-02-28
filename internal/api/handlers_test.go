package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/accelbench/accelbench/internal/database"

	"k8s.io/client-go/kubernetes/fake"
)

func seedRepo() *database.MockRepo {
	repo := database.NewMockRepo()
	repo.SeedModel(&database.Model{
		ID:         "model-001",
		HfID:       "meta-llama/Llama-3.1-8B",
		HfRevision: "abc123",
		CreatedAt:  time.Now(),
	})
	repo.SeedInstanceType(&database.InstanceType{
		ID:                   "inst-001",
		Name:                 "g5.xlarge",
		Family:               "g5",
		AcceleratorType:      "gpu",
		AcceleratorName:      "A10G",
		AcceleratorCount:     1,
		AcceleratorMemoryGiB: 24,
		VCPUs:                4,
		MemoryGiB:            16,
	})
	return repo
}

func setupServer() (*Server, *http.ServeMux) {
	repo := seedRepo()
	client := fake.NewSimpleClientset()
	srv := NewServer(repo, client)
	mux := http.NewServeMux()
	srv.RegisterRoutes(mux)
	return srv, mux
}

func TestHandleCreateRun_Success(t *testing.T) {
	_, mux := setupServer()

	body := database.RunRequest{
		ModelHfID:            "meta-llama/Llama-3.1-8B",
		ModelHfRevision:      "abc123",
		InstanceTypeName:     "g5.xlarge",
		Framework:            "vllm",
		FrameworkVersion:     "v0.6.0",
		TensorParallelDegree: 1,
		Concurrency:          16,
		InputSequenceLength:  512,
		OutputSequenceLength: 256,
		DatasetName:          "sharegpt",
		RunType:              "on_demand",
	}
	b, _ := json.Marshal(body)

	req := httptest.NewRequest("POST", "/api/v1/runs", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	mux.ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Errorf("status = %d, want %d; body: %s", w.Code, http.StatusAccepted, w.Body.String())
	}

	var resp map[string]string
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["id"] == "" {
		t.Error("response missing run id")
	}
	if resp["status"] != "pending" {
		t.Errorf("status = %s, want pending", resp["status"])
	}
}

func TestHandleCreateRun_InvalidJSON(t *testing.T) {
	_, mux := setupServer()

	req := httptest.NewRequest("POST", "/api/v1/runs", bytes.NewReader([]byte("not json")))
	w := httptest.NewRecorder()

	mux.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandleCreateRun_UnknownModelAutoRegisters(t *testing.T) {
	_, mux := setupServer()

	body := database.RunRequest{
		ModelHfID:       "nonexistent/model",
		ModelHfRevision: "abc",
		InstanceTypeName: "g5.xlarge",
		Framework:       "vllm",
		FrameworkVersion: "v0.6.0",
		Concurrency:     1,
		InputSequenceLength: 512,
		OutputSequenceLength: 256,
		DatasetName:     "sharegpt",
		RunType:         "on_demand",
	}
	b, _ := json.Marshal(body)

	req := httptest.NewRequest("POST", "/api/v1/runs", bytes.NewReader(b))
	w := httptest.NewRecorder()

	mux.ServeHTTP(w, req)

	// EnsureModel auto-registers unknown models, so this should succeed.
	if w.Code != http.StatusAccepted {
		t.Errorf("status = %d, want %d", w.Code, http.StatusAccepted)
	}
}

func TestHandleCreateRun_InstanceNotFound(t *testing.T) {
	_, mux := setupServer()

	body := database.RunRequest{
		ModelHfID:       "meta-llama/Llama-3.1-8B",
		ModelHfRevision: "abc123",
		InstanceTypeName: "nonexistent.xlarge",
		Framework:       "vllm",
		FrameworkVersion: "v0.6.0",
		Concurrency:     1,
		InputSequenceLength: 512,
		OutputSequenceLength: 256,
		DatasetName:     "sharegpt",
		RunType:         "on_demand",
	}
	b, _ := json.Marshal(body)

	req := httptest.NewRequest("POST", "/api/v1/runs", bytes.NewReader(b))
	w := httptest.NewRecorder()

	mux.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestHandleGetRun_Found(t *testing.T) {
	repo := seedRepo()
	client := fake.NewSimpleClientset()
	srv := NewServer(repo, client)
	mux := http.NewServeMux()
	srv.RegisterRoutes(mux)

	// Create a run directly in the mock repo.
	run := &database.BenchmarkRun{
		ModelID: "model-001", InstanceTypeID: "inst-001",
		Framework: "vllm", FrameworkVersion: "v0.6.0",
		Concurrency: 16, InputSequenceLength: 512,
		OutputSequenceLength: 256, DatasetName: "sharegpt",
		RunType: "on_demand", Status: "pending",
	}
	runID, _ := repo.CreateBenchmarkRun(context.Background(), run)

	req := httptest.NewRequest("GET", "/api/v1/runs/"+runID, nil)
	w := httptest.NewRecorder()

	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var resp database.BenchmarkRun
	json.NewDecoder(w.Body).Decode(&resp)
	if resp.ID != runID {
		t.Errorf("run id = %s, want %s", resp.ID, runID)
	}
}

func TestHandleGetRun_NotFound(t *testing.T) {
	_, mux := setupServer()

	req := httptest.NewRequest("GET", "/api/v1/runs/nonexistent", nil)
	w := httptest.NewRecorder()

	mux.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestHandleGetMetrics_NotFound(t *testing.T) {
	_, mux := setupServer()

	req := httptest.NewRequest("GET", "/api/v1/runs/nonexistent/metrics", nil)
	w := httptest.NewRecorder()

	mux.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestHandleGetMetrics_Found(t *testing.T) {
	repo := seedRepo()
	client := fake.NewSimpleClientset()
	srv := NewServer(repo, client)
	mux := http.NewServeMux()
	srv.RegisterRoutes(mux)

	// Seed a run + metrics.
	run := &database.BenchmarkRun{
		ModelID: "model-001", InstanceTypeID: "inst-001",
		Framework: "vllm", FrameworkVersion: "v0.6.0",
		Concurrency: 16, InputSequenceLength: 512,
		OutputSequenceLength: 256, DatasetName: "sharegpt",
		RunType: "on_demand", Status: "pending",
	}
	runID, _ := repo.CreateBenchmarkRun(context.Background(), run)

	ttft := 42.0
	repo.PersistMetrics(context.Background(), runID, &database.BenchmarkMetrics{
		TTFTP50Ms: &ttft,
	})

	req := httptest.NewRequest("GET", "/api/v1/runs/"+runID+"/metrics", nil)
	w := httptest.NewRecorder()

	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d; body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	var resp database.BenchmarkMetrics
	json.NewDecoder(w.Body).Decode(&resp)
	if resp.TTFTP50Ms == nil || *resp.TTFTP50Ms != 42.0 {
		t.Errorf("ttft_p50 = %v, want 42.0", resp.TTFTP50Ms)
	}
}

// --- Catalog API tests ---

func seedCatalogServer() (*database.MockRepo, *http.ServeMux) {
	repo := seedRepo()
	client := fake.NewSimpleClientset()

	// Add a second instance type and model for catalog variety.
	llama := "llama"
	repo.SeedModel(&database.Model{
		ID: "model-002", HfID: "meta-llama/Llama-3.1-70B", HfRevision: "def456",
		ModelFamily: &llama, CreatedAt: time.Now(),
	})
	repo.SeedInstanceType(&database.InstanceType{
		ID: "inst-002", Name: "p5.48xlarge", Family: "p5",
		AcceleratorType: "gpu", AcceleratorName: "H100",
		AcceleratorCount: 8, AcceleratorMemoryGiB: 640,
		VCPUs: 192, MemoryGiB: 2048,
	})
	repo.SeedInstanceType(&database.InstanceType{
		ID: "inst-003", Name: "inf2.xlarge", Family: "inf2",
		AcceleratorType: "neuron", AcceleratorName: "Inferentia2",
		AcceleratorCount: 2, AcceleratorMemoryGiB: 32,
		VCPUs: 4, MemoryGiB: 16,
	})

	ctx := context.Background()
	ttft := 30.0

	// Catalog runs.
	catalogRuns := []struct {
		modelID, instID string
	}{
		{"model-001", "inst-001"},
		{"model-002", "inst-002"},
		{"model-001", "inst-003"},
	}
	for _, cr := range catalogRuns {
		run := &database.BenchmarkRun{
			ModelID: cr.modelID, InstanceTypeID: cr.instID,
			Framework: "vllm", FrameworkVersion: "v0.6.0",
			TensorParallelDegree: 1, Concurrency: 16,
			InputSequenceLength: 512, OutputSequenceLength: 256,
			DatasetName: "sharegpt", RunType: "catalog", Status: "pending",
		}
		id, _ := repo.CreateBenchmarkRun(ctx, run)
		repo.PersistMetrics(ctx, id, &database.BenchmarkMetrics{TTFTP50Ms: &ttft})
	}

	srv := NewServer(repo, client)
	mux := http.NewServeMux()
	srv.RegisterRoutes(mux)
	return repo, mux
}

func TestHandleListCatalog_All(t *testing.T) {
	_, mux := seedCatalogServer()

	req := httptest.NewRequest("GET", "/api/v1/catalog", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var entries []database.CatalogEntry
	json.NewDecoder(w.Body).Decode(&entries)
	if len(entries) != 3 {
		t.Errorf("got %d entries, want 3", len(entries))
	}
}

func TestHandleListCatalog_FilterModel(t *testing.T) {
	_, mux := seedCatalogServer()

	req := httptest.NewRequest("GET", "/api/v1/catalog?model=meta-llama/Llama-3.1-8B", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var entries []database.CatalogEntry
	json.NewDecoder(w.Body).Decode(&entries)
	if len(entries) != 2 {
		t.Errorf("got %d entries, want 2", len(entries))
	}
}

func TestHandleListCatalog_FilterAcceleratorType(t *testing.T) {
	_, mux := seedCatalogServer()

	req := httptest.NewRequest("GET", "/api/v1/catalog?accelerator_type=neuron", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var entries []database.CatalogEntry
	json.NewDecoder(w.Body).Decode(&entries)
	if len(entries) != 1 {
		t.Errorf("got %d entries, want 1", len(entries))
	}
}

func TestHandleListCatalog_FilterInstanceFamily(t *testing.T) {
	_, mux := seedCatalogServer()

	req := httptest.NewRequest("GET", "/api/v1/catalog?instance_family=p5", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var entries []database.CatalogEntry
	json.NewDecoder(w.Body).Decode(&entries)
	if len(entries) != 1 {
		t.Errorf("got %d entries, want 1", len(entries))
	}
}

func TestHandleListCatalog_Pagination(t *testing.T) {
	_, mux := seedCatalogServer()

	req := httptest.NewRequest("GET", "/api/v1/catalog?limit=2", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	var entries []database.CatalogEntry
	json.NewDecoder(w.Body).Decode(&entries)
	if len(entries) != 2 {
		t.Errorf("got %d entries, want 2", len(entries))
	}
}

func TestHandleListCatalog_Empty(t *testing.T) {
	repo := database.NewMockRepo()
	client := fake.NewSimpleClientset()
	srv := NewServer(repo, client)
	mux := http.NewServeMux()
	srv.RegisterRoutes(mux)

	req := httptest.NewRequest("GET", "/api/v1/catalog", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var entries []database.CatalogEntry
	json.NewDecoder(w.Body).Decode(&entries)
	if len(entries) != 0 {
		t.Errorf("got %d entries, want 0", len(entries))
	}
}

// --- Jobs (ListRuns / Cancel / Delete) API tests ---

func seedJobsServer() (*database.MockRepo, *http.ServeMux) {
	repo := seedRepo()
	client := fake.NewSimpleClientset()

	ctx := context.Background()
	for _, status := range []string{"pending", "running", "completed", "failed"} {
		run := &database.BenchmarkRun{
			ModelID: "model-001", InstanceTypeID: "inst-001",
			Framework: "vllm", FrameworkVersion: "0.4.0",
			TensorParallelDegree: 1, Concurrency: 16,
			InputSequenceLength: 128, OutputSequenceLength: 128,
			DatasetName: "synthetic", RunType: "on_demand",
			Status: status,
		}
		repo.CreateBenchmarkRun(ctx, run)
	}

	srv := NewServer(repo, client)
	mux := http.NewServeMux()
	srv.RegisterRoutes(mux)
	return repo, mux
}

func TestHandleListRuns_All(t *testing.T) {
	_, mux := seedJobsServer()

	req := httptest.NewRequest("GET", "/api/v1/jobs", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var items []database.RunListItem
	json.NewDecoder(w.Body).Decode(&items)
	if len(items) != 4 {
		t.Errorf("expected 4 items, got %d", len(items))
	}
}

func TestHandleListRuns_FilterByStatus(t *testing.T) {
	_, mux := seedJobsServer()

	req := httptest.NewRequest("GET", "/api/v1/jobs?status=completed", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var items []database.RunListItem
	json.NewDecoder(w.Body).Decode(&items)
	if len(items) != 1 {
		t.Errorf("expected 1 completed item, got %d", len(items))
	}
}

func TestHandleListRuns_FilterByModel(t *testing.T) {
	_, mux := seedJobsServer()

	req := httptest.NewRequest("GET", "/api/v1/jobs?model=llama", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var items []database.RunListItem
	json.NewDecoder(w.Body).Decode(&items)
	if len(items) != 4 {
		t.Errorf("expected 4 llama items, got %d", len(items))
	}
}

func TestHandleListRuns_Empty(t *testing.T) {
	repo := database.NewMockRepo()
	client := fake.NewSimpleClientset()
	srv := NewServer(repo, client)
	mux := http.NewServeMux()
	srv.RegisterRoutes(mux)

	req := httptest.NewRequest("GET", "/api/v1/jobs", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var items []database.RunListItem
	json.NewDecoder(w.Body).Decode(&items)
	if len(items) != 0 {
		t.Errorf("expected 0 items, got %d", len(items))
	}
}

func TestHandleListRuns_Pagination(t *testing.T) {
	_, mux := seedJobsServer()

	req := httptest.NewRequest("GET", "/api/v1/jobs?limit=2&offset=0", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var items []database.RunListItem
	json.NewDecoder(w.Body).Decode(&items)
	if len(items) != 2 {
		t.Errorf("expected 2 items with limit=2, got %d", len(items))
	}
}

func TestHandleCancelRun_NotFound(t *testing.T) {
	_, mux := seedJobsServer()

	req := httptest.NewRequest("POST", "/api/v1/runs/nonexistent/cancel", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
}

func TestHandleCancelRun_AlreadyCompleted(t *testing.T) {
	repo, mux := seedJobsServer()

	items, _ := repo.ListRuns(nil, database.RunFilter{Status: "completed"})
	if len(items) == 0 {
		t.Fatal("no completed runs")
	}

	req := httptest.NewRequest("POST", "/api/v1/runs/"+items[0].ID+"/cancel", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", w.Code)
	}
}

func TestHandleCancelRun_Success(t *testing.T) {
	repo, mux := seedJobsServer()

	items, _ := repo.ListRuns(nil, database.RunFilter{Status: "running"})
	if len(items) == 0 {
		t.Fatal("no running runs")
	}

	req := httptest.NewRequest("POST", "/api/v1/runs/"+items[0].ID+"/cancel", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	if got := repo.GetRunStatus(items[0].ID); got != "failed" {
		t.Errorf("status = %s, want failed", got)
	}
}

func TestHandleDeleteRun_NotFound(t *testing.T) {
	_, mux := seedJobsServer()

	req := httptest.NewRequest("DELETE", "/api/v1/runs/nonexistent", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
}

func TestHandleDeleteRun_Success(t *testing.T) {
	repo, mux := seedJobsServer()

	items, _ := repo.ListRuns(nil, database.RunFilter{Status: "completed"})
	if len(items) == 0 {
		t.Fatal("no completed runs")
	}
	runID := items[0].ID

	req := httptest.NewRequest("DELETE", "/api/v1/runs/"+runID, nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", w.Code)
	}

	run, _ := repo.GetBenchmarkRun(nil, runID)
	if run != nil {
		t.Error("expected run to be deleted")
	}
}

func TestHandleDeleteRun_CancelsActiveRun(t *testing.T) {
	repo, mux := seedJobsServer()

	items, _ := repo.ListRuns(nil, database.RunFilter{Status: "running"})
	if len(items) == 0 {
		t.Fatal("no running runs")
	}
	runID := items[0].ID

	req := httptest.NewRequest("DELETE", "/api/v1/runs/"+runID, nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", w.Code)
	}

	run, _ := repo.GetBenchmarkRun(nil, runID)
	if run != nil {
		t.Error("expected run to be deleted")
	}
}
