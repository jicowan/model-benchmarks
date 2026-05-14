package database

import (
	"context"
	"testing"
	"time"
)

func seedMockRepoWithRuns(t *testing.T) *MockRepo {
	t.Helper()
	repo := NewMockRepo()

	// Seed two models.
	repo.SeedModel(&Model{
		ID:         "model-001",
		HfID:       "meta-llama/Llama-3-8B",
		HfRevision: "main",
		CreatedAt:  time.Now(),
	})
	repo.SeedModel(&Model{
		ID:         "model-002",
		HfID:       "mistralai/Mistral-7B-v0.1",
		HfRevision: "main",
		CreatedAt:  time.Now(),
	})

	// Seed instance type.
	repo.SeedInstanceType(&InstanceType{
		ID:                   "it-001",
		Name:                 "ml.g5.2xlarge",
		Family:               "g5",
		AcceleratorType:      "gpu",
		AcceleratorName:      "A10G",
		AcceleratorCount:     1,
		AcceleratorMemoryGiB: 24,
		VCPUs:                8,
		MemoryGiB:            32,
	})

	ctx := context.Background()

	// Create runs in different statuses.
	for _, tc := range []struct {
		modelID string
		status  string
	}{
		{"model-001", "completed"},
		{"model-001", "running"},
		{"model-002", "pending"},
		{"model-002", "failed"},
	} {
		run := &BenchmarkRun{
			ModelID:              tc.modelID,
			InstanceTypeID:       "it-001",
			Framework:            "vllm",
			FrameworkVersion:     "0.4.0",
			TensorParallelDegree: 1,
			Concurrency:          16,
			InputSequenceLength:  128,
			OutputSequenceLength: 128,
			DatasetName:          "synthetic",
			RunType:              "on_demand",
			Status:               tc.status,
		}
		_, err := repo.CreateBenchmarkRun(ctx, run)
		if err != nil {
			t.Fatalf("create run: %v", err)
		}
	}

	return repo
}

func TestListRuns_NoFilter(t *testing.T) {
	repo := seedMockRepoWithRuns(t)
	ctx := context.Background()

	items, err := repo.ListRuns(ctx, RunFilter{})
	if err != nil {
		t.Fatalf("ListRuns: %v", err)
	}
	if len(items) != 4 {
		t.Errorf("expected 4 runs, got %d", len(items))
	}
}

func TestListRuns_FilterByStatus(t *testing.T) {
	repo := seedMockRepoWithRuns(t)
	ctx := context.Background()

	items, err := repo.ListRuns(ctx, RunFilter{Status: "completed"})
	if err != nil {
		t.Fatalf("ListRuns: %v", err)
	}
	if len(items) != 1 {
		t.Errorf("expected 1 completed run, got %d", len(items))
	}
	if items[0].Status != "completed" {
		t.Errorf("expected status completed, got %s", items[0].Status)
	}
}

func TestListRuns_FilterByModel(t *testing.T) {
	repo := seedMockRepoWithRuns(t)
	ctx := context.Background()

	items, err := repo.ListRuns(ctx, RunFilter{ModelID: "llama"})
	if err != nil {
		t.Fatalf("ListRuns: %v", err)
	}
	if len(items) != 2 {
		t.Errorf("expected 2 llama runs, got %d", len(items))
	}
	for _, item := range items {
		if item.ModelHfID != "meta-llama/Llama-3-8B" {
			t.Errorf("unexpected model: %s", item.ModelHfID)
		}
	}
}

func TestListRuns_Pagination(t *testing.T) {
	repo := seedMockRepoWithRuns(t)
	ctx := context.Background()

	items, err := repo.ListRuns(ctx, RunFilter{Limit: 2})
	if err != nil {
		t.Fatalf("ListRuns: %v", err)
	}
	if len(items) != 2 {
		t.Errorf("expected 2 runs with limit 2, got %d", len(items))
	}

	items2, err := repo.ListRuns(ctx, RunFilter{Limit: 2, Offset: 2})
	if err != nil {
		t.Fatalf("ListRuns: %v", err)
	}
	if len(items2) != 2 {
		t.Errorf("expected 2 runs with offset 2, got %d", len(items2))
	}
}

func TestListRuns_OffsetBeyondTotal(t *testing.T) {
	repo := seedMockRepoWithRuns(t)
	ctx := context.Background()

	items, err := repo.ListRuns(ctx, RunFilter{Offset: 100})
	if err != nil {
		t.Fatalf("ListRuns: %v", err)
	}
	if items != nil {
		t.Errorf("expected nil for offset beyond total, got %d items", len(items))
	}
}

func TestDeleteRun(t *testing.T) {
	repo := seedMockRepoWithRuns(t)
	ctx := context.Background()

	// List runs to find an ID.
	items, err := repo.ListRuns(ctx, RunFilter{})
	if err != nil {
		t.Fatalf("ListRuns: %v", err)
	}
	if len(items) == 0 {
		t.Fatal("no runs to delete")
	}

	runID := items[0].ID

	// Seed some metrics for that run.
	_ = repo.PersistMetrics(ctx, runID, &BenchmarkMetrics{})

	// Delete.
	if err := repo.DeleteRun(ctx, runID); err != nil {
		t.Fatalf("DeleteRun: %v", err)
	}

	// Verify it's gone.
	run, err := repo.GetBenchmarkRun(ctx, runID)
	if err != nil {
		t.Fatalf("GetBenchmarkRun: %v", err)
	}
	if run != nil {
		t.Error("expected run to be deleted")
	}

	// Verify metrics are gone.
	met, err := repo.GetMetricsByRunID(ctx, runID)
	if err != nil {
		t.Fatalf("GetMetricsByRunID: %v", err)
	}
	if met != nil {
		t.Error("expected metrics to be deleted")
	}
}

func TestListRuns_ItemFields(t *testing.T) {
	repo := seedMockRepoWithRuns(t)
	ctx := context.Background()

	items, err := repo.ListRuns(ctx, RunFilter{Status: "pending"})
	if err != nil {
		t.Fatalf("ListRuns: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 pending run, got %d", len(items))
	}

	item := items[0]
	if item.ModelHfID != "mistralai/Mistral-7B-v0.1" {
		t.Errorf("expected mistral model, got %s", item.ModelHfID)
	}
	if item.InstanceTypeName != "ml.g5.2xlarge" {
		t.Errorf("expected ml.g5.2xlarge, got %s", item.InstanceTypeName)
	}
	if item.Framework != "vllm" {
		t.Errorf("expected vllm, got %s", item.Framework)
	}
	if item.RunType != "on_demand" {
		t.Errorf("expected on_demand, got %s", item.RunType)
	}
}

// PRD-50: streamer_mode / streamer_concurrency / streamer_memory_limit_gib
// round-trip through Create → GetRunExportDetails with UseRunaiStreamer
// resolved correctly.
func TestStreamerKnobs_RoundTrip(t *testing.T) {
	repo := seedMockRepoWithRuns(t)
	ctx := context.Background()

	mode := "auto"
	conc := 8
	memLimit := 12
	s3 := "s3://bucket/models/foo"

	id, err := repo.CreateBenchmarkRun(ctx, &BenchmarkRun{
		ModelID:                "model-001",
		InstanceTypeID:         "it-001",
		Framework:              "vllm",
		FrameworkVersion:       "v0.20.0",
		TensorParallelDegree:   1,
		Concurrency:            4,
		InputSequenceLength:    512,
		OutputSequenceLength:   256,
		DatasetName:            "synthetic",
		RunType:                "on_demand",
		Status:                 "pending",
		ModelS3URI:             &s3,
		StreamerMode:           &mode,
		StreamerConcurrency:    &conc,
		StreamerMemoryLimitGiB: &memLimit,
	})
	if err != nil {
		t.Fatalf("CreateBenchmarkRun: %v", err)
	}

	details, err := repo.GetRunExportDetails(ctx, id)
	if err != nil || details == nil {
		t.Fatalf("GetRunExportDetails: %v, details=%v", err, details)
	}
	if details.StreamerMode == nil || *details.StreamerMode != "auto" {
		t.Errorf("StreamerMode = %v, want auto", details.StreamerMode)
	}
	if details.StreamerConcurrency == nil || *details.StreamerConcurrency != 8 {
		t.Errorf("StreamerConcurrency = %v, want 8", details.StreamerConcurrency)
	}
	if details.StreamerMemoryLimitGiB == nil || *details.StreamerMemoryLimitGiB != 12 {
		t.Errorf("StreamerMemoryLimitGiB = %v, want 12", details.StreamerMemoryLimitGiB)
	}
	if !details.UseRunaiStreamer {
		t.Error("UseRunaiStreamer = false, want true (auto + s3)")
	}
}

// streamer_mode=off must disable the streamer even when the model is S3-backed.
func TestStreamerKnobs_ModeOffDisables(t *testing.T) {
	repo := seedMockRepoWithRuns(t)
	ctx := context.Background()

	mode := "off"
	s3 := "s3://bucket/models/foo"

	id, err := repo.CreateBenchmarkRun(ctx, &BenchmarkRun{
		ModelID:              "model-001",
		InstanceTypeID:       "it-001",
		Framework:            "vllm",
		FrameworkVersion:     "v0.20.0",
		TensorParallelDegree: 1,
		Concurrency:          4,
		InputSequenceLength:  512,
		OutputSequenceLength: 256,
		DatasetName:          "synthetic",
		RunType:              "on_demand",
		Status:               "pending",
		ModelS3URI:           &s3,
		StreamerMode:         &mode,
	})
	if err != nil {
		t.Fatalf("CreateBenchmarkRun: %v", err)
	}
	details, err := repo.GetRunExportDetails(ctx, id)
	if err != nil || details == nil {
		t.Fatalf("GetRunExportDetails: %v, details=%v", err, details)
	}
	if details.UseRunaiStreamer {
		t.Error("UseRunaiStreamer = true, want false (streamer_mode=off)")
	}
}
