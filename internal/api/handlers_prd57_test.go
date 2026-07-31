package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/accelbench/accelbench/internal/database"

	"k8s.io/client-go/kubernetes/fake"
)

// distServer seeds a repo with a multi-GPU instance type (8 GPUs/node) so the
// PRD-57 distributed validation (TP == GPUs/node, PP == node_count) can be
// exercised, and returns a wired server.
func distServer(t *testing.T) *http.ServeMux {
	t.Helper()
	repo := database.NewMockRepo()
	repo.SeedModel(&database.Model{ID: "m-70b", HfID: "meta-llama/Llama-3.1-70B", HfRevision: "main"})
	repo.SeedInstanceType(&database.InstanceType{
		ID: "inst-p5", Name: "p5.48xlarge", Family: "p5",
		AcceleratorType: "gpu", AcceleratorName: "H100",
		AcceleratorCount: 8, AcceleratorMemoryGiB: 640, VCPUs: 192, MemoryGiB: 2048,
	})
	// A single-GPU GPU type for the "not enough nodes / wrong TP" edges reuse.
	srv := NewServer(repo, fake.NewSimpleClientset(), "test-pod")
	mux := http.NewServeMux()
	srv.RegisterRoutes(mux)
	return mux
}

func postRun(mux *http.ServeMux, body database.RunRequest) *httptest.ResponseRecorder {
	b, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", "/api/v1/runs", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	return w
}

func validDistributedReq() database.RunRequest {
	return database.RunRequest{
		ModelHfID:              "meta-llama/Llama-3.1-70B",
		InstanceTypeName:       "p5.48xlarge",
		Framework:              "llm-d",
		FrameworkVersion:       "0.2.0",
		TensorParallelDegree:   8, // == GPUs/node
		Concurrency:            16,
		InputSequenceLength:    512,
		OutputSequenceLength:   256,
		DatasetName:            "sharegpt",
		RunType:                "on_demand",
		ScenarioID:             "chatbot",
		DeploymentMode:         "distributed",
		NodeCount:              2,
		PipelineParallelDegree: 2, // == node_count
		NetworkMode:            "tcp",
	}
}

func TestCreateRun_Distributed_Accepted(t *testing.T) {
	mux := distServer(t)
	w := postRun(mux, validDistributedReq())
	if w.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body: %s", w.Code, w.Body.String())
	}
}

func TestCreateRun_Distributed_RejectsNonLLMD(t *testing.T) {
	mux := distServer(t)
	r := validDistributedReq()
	r.Framework = "vllm"
	w := postRun(mux, r)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "framework=llm-d") {
		t.Errorf("want 400 framework=llm-d; got %d: %s", w.Code, w.Body.String())
	}
}

func TestCreateRun_Distributed_RejectsSingleNode(t *testing.T) {
	mux := distServer(t)
	r := validDistributedReq()
	r.NodeCount = 1
	r.PipelineParallelDegree = 1
	w := postRun(mux, r)
	// Note: writeError JSON-escapes ">=" to >=, so match on the stable prefix.
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "node_count") {
		t.Errorf("want 400 node_count; got %d: %s", w.Code, w.Body.String())
	}
}

func TestCreateRun_Distributed_RejectsPPMismatch(t *testing.T) {
	mux := distServer(t)
	r := validDistributedReq()
	r.PipelineParallelDegree = 3 // != node_count (2)
	w := postRun(mux, r)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "pipeline_parallel_degree") {
		t.Errorf("want 400 PP mismatch; got %d: %s", w.Code, w.Body.String())
	}
}

func TestCreateRun_Distributed_RejectsTPOverGPUs(t *testing.T) {
	mux := distServer(t)
	r := validDistributedReq()
	r.TensorParallelDegree = 16 // > GPUs/node (8) — physically impossible
	w := postRun(mux, r)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "tensor_parallel_degree") {
		t.Errorf("want 400 TP > GPUs/node; got %d: %s", w.Code, w.Body.String())
	}
}

// TP is an INDEPENDENT knob, not forced to fill the node: TP < GPUs/node is
// allowed (e.g. TP=4 on an 8-GPU node), and TP=1 (pipeline-parallel WITHOUT
// tensor-parallel) is first-class.
func TestCreateRun_Distributed_AllowsTPBelowGPUs(t *testing.T) {
	mux := distServer(t)
	r := validDistributedReq()
	r.TensorParallelDegree = 4 // < GPUs/node (8) — valid
	if w := postRun(mux, r); w.Code != http.StatusAccepted {
		t.Errorf("TP<GPUs/node should be accepted; got %d: %s", w.Code, w.Body.String())
	}
}

func TestCreateRun_Distributed_AllowsPPWithoutTP(t *testing.T) {
	mux := distServer(t)
	r := validDistributedReq()
	r.TensorParallelDegree = 1 // PP-only, no within-node tensor sharding
	if w := postRun(mux, r); w.Code != http.StatusAccepted {
		t.Errorf("TP=1 (PP-only) should be accepted; got %d: %s", w.Code, w.Body.String())
	}
}

func TestCreateRun_Distributed_PersistsTopology(t *testing.T) {
	repo := database.NewMockRepo()
	repo.SeedModel(&database.Model{ID: "m-70b", HfID: "meta-llama/Llama-3.1-70B", HfRevision: "main"})
	repo.SeedInstanceType(&database.InstanceType{
		ID: "inst-p5", Name: "p5.48xlarge", Family: "p5",
		AcceleratorType: "gpu", AcceleratorName: "H100",
		AcceleratorCount: 8, AcceleratorMemoryGiB: 640, VCPUs: 192, MemoryGiB: 2048,
	})
	srv := NewServer(repo, fake.NewSimpleClientset(), "test-pod")

	runID, err := srv.CreateRun(context.Background(), ptrReq(validDistributedReq()))
	if err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	got, err := repo.GetBenchmarkRun(context.Background(), runID)
	if err != nil || got == nil {
		t.Fatalf("GetBenchmarkRun: %v", err)
	}
	if got.DeploymentMode == nil || *got.DeploymentMode != "distributed" {
		t.Errorf("deployment_mode not persisted: %v", got.DeploymentMode)
	}
	if got.NodeCount == nil || *got.NodeCount != 2 {
		t.Errorf("node_count not persisted: %v", got.NodeCount)
	}
	if got.PipelineParallelDegree == nil || *got.PipelineParallelDegree != 2 {
		t.Errorf("pipeline_parallel_degree not persisted: %v", got.PipelineParallelDegree)
	}
	if got.NetworkMode == nil || *got.NetworkMode != "tcp" {
		t.Errorf("network_mode not persisted: %v", got.NetworkMode)
	}
}

// TestCreateRun_SingleNode_LeavesTopologyNull guards the regression contract:
// a normal single-instance run persists NULL topology (unchanged behavior).
func TestCreateRun_SingleNode_LeavesTopologyNull(t *testing.T) {
	repo := database.NewMockRepo()
	repo.SeedModel(&database.Model{ID: "model-001", HfID: "meta-llama/Llama-3.1-8B", HfRevision: "abc123"})
	repo.SeedInstanceType(&database.InstanceType{
		ID: "inst-001", Name: "g5.xlarge", Family: "g5",
		AcceleratorType: "gpu", AcceleratorName: "A10G",
		AcceleratorCount: 1, AcceleratorMemoryGiB: 24, VCPUs: 4, MemoryGiB: 16,
	})
	srv := NewServer(repo, fake.NewSimpleClientset(), "test-pod")

	runID, err := srv.CreateRun(context.Background(), &database.RunRequest{
		ModelHfID: "meta-llama/Llama-3.1-8B", InstanceTypeName: "g5.xlarge",
		Framework: "vllm", FrameworkVersion: "v0.6.0", TensorParallelDegree: 1,
		Concurrency: 16, InputSequenceLength: 512, OutputSequenceLength: 256,
		DatasetName: "sharegpt", RunType: "on_demand", ScenarioID: "chatbot",
	})
	if err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	got, _ := repo.GetBenchmarkRun(context.Background(), runID)
	if got.DeploymentMode != nil || got.NodeCount != nil || got.PipelineParallelDegree != nil || got.NetworkMode != nil {
		t.Errorf("single-node run should have NULL topology; got mode=%v nc=%v pp=%v nm=%v",
			got.DeploymentMode, got.NodeCount, got.PipelineParallelDegree, got.NetworkMode)
	}
}

func ptrReq(r database.RunRequest) *database.RunRequest { return &r }
