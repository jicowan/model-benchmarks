package database

import (
	"context"
	"testing"
)

// seedCatalogRepo creates a MockRepo pre-loaded with models, instance types,
// and completed catalog benchmark runs with metrics.
func seedCatalogRepo() *MockRepo {
	repo := NewMockRepo()

	llama := "llama"
	mistral := "mistral"
	paramSmall := int64(8000000000)
	paramLarge := int64(70000000000)

	repo.SeedModel(&Model{ID: "m1", HfID: "meta-llama/Llama-3.1-8B", HfRevision: "abc", ModelFamily: &llama, ParameterCount: &paramSmall})
	repo.SeedModel(&Model{ID: "m2", HfID: "meta-llama/Llama-3.1-70B", HfRevision: "def", ModelFamily: &llama, ParameterCount: &paramLarge})
	repo.SeedModel(&Model{ID: "m3", HfID: "mistralai/Mistral-7B", HfRevision: "ghi", ModelFamily: &mistral, ParameterCount: &paramSmall})

	repo.SeedInstanceType(&InstanceType{ID: "i1", Name: "g5.xlarge", Family: "g5", AcceleratorType: "gpu", AcceleratorName: "A10G", AcceleratorCount: 1, AcceleratorMemoryGiB: 24, VCPUs: 4, MemoryGiB: 16})
	repo.SeedInstanceType(&InstanceType{ID: "i2", Name: "p5.48xlarge", Family: "p5", AcceleratorType: "gpu", AcceleratorName: "H100", AcceleratorCount: 8, AcceleratorMemoryGiB: 640, VCPUs: 192, MemoryGiB: 2048})
	repo.SeedInstanceType(&InstanceType{ID: "i3", Name: "inf2.xlarge", Family: "inf2", AcceleratorType: "neuron", AcceleratorName: "Inferentia2", AcceleratorCount: 2, AcceleratorMemoryGiB: 32, VCPUs: 4, MemoryGiB: 16})

	ctx := context.Background()

	// Completed catalog runs.
	runs := []struct {
		modelID, instID, framework string
	}{
		{"m1", "i1", "vllm"},
		{"m2", "i2", "vllm"},
		{"m1", "i3", "vllm-neuron"},
		{"m3", "i1", "vllm"},
	}

	ttft := 25.0
	for _, r := range runs {
		run := &BenchmarkRun{
			ModelID: r.modelID, InstanceTypeID: r.instID,
			Framework: r.framework, FrameworkVersion: "v0.6.0",
			TensorParallelDegree: 1, Concurrency: 16,
			InputSequenceLength: 512, OutputSequenceLength: 256,
			DatasetName: "sharegpt", RunType: "catalog", Status: "pending",
		}
		id, _ := repo.CreateBenchmarkRun(ctx, run)
		repo.PersistMetrics(ctx, id, &BenchmarkMetrics{TTFTP50Ms: &ttft})
	}

	// Also add an on_demand run (now included in catalog).
	odRun := &BenchmarkRun{
		ModelID: "m1", InstanceTypeID: "i1",
		Framework: "vllm", FrameworkVersion: "v0.6.0",
		TensorParallelDegree: 1, Concurrency: 16,
		InputSequenceLength: 512, OutputSequenceLength: 256,
		DatasetName: "sharegpt", RunType: "on_demand", Status: "pending",
	}
	odID, _ := repo.CreateBenchmarkRun(ctx, odRun)
	repo.PersistMetrics(ctx, odID, &BenchmarkMetrics{TTFTP50Ms: &ttft})

	return repo
}

func TestListCatalog_AllEntries(t *testing.T) {
	repo := seedCatalogRepo()
	entries, err := repo.ListCatalog(context.Background(), CatalogFilter{})
	if err != nil {
		t.Fatalf("ListCatalog: %v", err)
	}
	// 5 completed runs (4 catalog + 1 on_demand).
	if len(entries) != 5 {
		t.Errorf("got %d entries, want 5", len(entries))
	}
}

func TestListCatalog_FilterByModel(t *testing.T) {
	repo := seedCatalogRepo()
	entries, err := repo.ListCatalog(context.Background(), CatalogFilter{
		ModelHfID: "meta-llama/Llama-3.1-8B",
	})
	if err != nil {
		t.Fatalf("ListCatalog: %v", err)
	}
	// 8B on g5 (catalog) + 8B on inf2 (catalog) + 8B on g5 (on_demand) = 3
	if len(entries) != 3 {
		t.Errorf("got %d entries, want 3 (g5 catalog + inf2 + g5 on_demand)", len(entries))
	}
	for _, e := range entries {
		if e.ModelHfID != "meta-llama/Llama-3.1-8B" {
			t.Errorf("unexpected model: %s", e.ModelHfID)
		}
	}
}

func TestListCatalog_FilterByModelFamily(t *testing.T) {
	repo := seedCatalogRepo()
	entries, err := repo.ListCatalog(context.Background(), CatalogFilter{
		ModelFamily: "mistral",
	})
	if err != nil {
		t.Fatalf("ListCatalog: %v", err)
	}
	if len(entries) != 1 {
		t.Errorf("got %d entries, want 1", len(entries))
	}
}

func TestListCatalog_FilterByInstanceFamily(t *testing.T) {
	repo := seedCatalogRepo()
	entries, err := repo.ListCatalog(context.Background(), CatalogFilter{
		InstanceFamily: "p5",
	})
	if err != nil {
		t.Fatalf("ListCatalog: %v", err)
	}
	if len(entries) != 1 {
		t.Errorf("got %d entries, want 1", len(entries))
	}
	if len(entries) > 0 && entries[0].InstanceTypeName != "p5.48xlarge" {
		t.Errorf("instance = %s, want p5.48xlarge", entries[0].InstanceTypeName)
	}
}

func TestListCatalog_FilterByAcceleratorType(t *testing.T) {
	repo := seedCatalogRepo()
	entries, err := repo.ListCatalog(context.Background(), CatalogFilter{
		AcceleratorType: "neuron",
	})
	if err != nil {
		t.Fatalf("ListCatalog: %v", err)
	}
	if len(entries) != 1 {
		t.Errorf("got %d entries, want 1", len(entries))
	}
	if len(entries) > 0 && entries[0].AcceleratorType != "neuron" {
		t.Errorf("accelerator_type = %s, want neuron", entries[0].AcceleratorType)
	}
}

func TestListCatalog_CombinedFilters(t *testing.T) {
	repo := seedCatalogRepo()
	entries, err := repo.ListCatalog(context.Background(), CatalogFilter{
		ModelFamily:     "llama",
		AcceleratorType: "gpu",
	})
	if err != nil {
		t.Fatalf("ListCatalog: %v", err)
	}
	// llama models on GPU: 8B on g5 (catalog) + 70B on p5 + 8B on g5 (on_demand) = 3
	if len(entries) != 3 {
		t.Errorf("got %d entries, want 3", len(entries))
	}
}

func TestListCatalog_Limit(t *testing.T) {
	repo := seedCatalogRepo()
	entries, err := repo.ListCatalog(context.Background(), CatalogFilter{
		Limit: 2,
	})
	if err != nil {
		t.Fatalf("ListCatalog: %v", err)
	}
	if len(entries) != 2 {
		t.Errorf("got %d entries, want 2", len(entries))
	}
}

func TestListCatalog_Offset(t *testing.T) {
	repo := seedCatalogRepo()
	all, _ := repo.ListCatalog(context.Background(), CatalogFilter{})
	paged, err := repo.ListCatalog(context.Background(), CatalogFilter{Offset: 2, Limit: 100})
	if err != nil {
		t.Fatalf("ListCatalog: %v", err)
	}
	if len(paged) != len(all)-2 {
		t.Errorf("got %d entries, want %d", len(paged), len(all)-2)
	}
}

func TestListCatalog_MetricsPresent(t *testing.T) {
	repo := seedCatalogRepo()
	entries, err := repo.ListCatalog(context.Background(), CatalogFilter{Limit: 1})
	if err != nil {
		t.Fatalf("ListCatalog: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("expected at least 1 entry")
	}
	if entries[0].TTFTP50Ms == nil {
		t.Error("ttft_p50 should not be nil for completed catalog entry")
	}
}

func TestListCatalog_Empty(t *testing.T) {
	repo := NewMockRepo()
	entries, err := repo.ListCatalog(context.Background(), CatalogFilter{})
	if err != nil {
		t.Fatalf("ListCatalog: %v", err)
	}
	if entries != nil && len(entries) != 0 {
		t.Errorf("expected empty, got %d", len(entries))
	}
}
