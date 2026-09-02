package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/accelbench/accelbench/internal/database"

	fake "k8s.io/client-go/kubernetes/fake"
)

// seedCPURepo seeds a Graviton CPU instance + a small model so the PRD-67 §11
// guard tests can drive CreateRun.
func seedCPURepo() *database.MockRepo {
	repo := database.NewMockRepo()
	repo.SeedModel(&database.Model{
		ID: "model-cpu", HfID: "Qwen/Qwen2.5-1.5B-Instruct", HfRevision: "main", CreatedAt: time.Now(),
	})
	repo.SeedInstanceType(&database.InstanceType{
		ID: "inst-cpu", Name: "r8g.16xlarge", Family: "r8g",
		AcceleratorType: "cpu", AcceleratorName: "Graviton4",
		AcceleratorCount: 0, AcceleratorMemoryGiB: 0, VCPUs: 64, MemoryGiB: 512,
	})
	// A GPU instance for the mirror (same knobs must still pass on GPU).
	repo.SeedInstanceType(&database.InstanceType{
		ID: "inst-gpu", Name: "g6.2xlarge", Family: "g6",
		AcceleratorType: "gpu", AcceleratorName: "L4",
		AcceleratorCount: 1, AcceleratorMemoryGiB: 24, VCPUs: 8, MemoryGiB: 32,
	})
	return repo
}

func cpuGuardServer() *Server {
	return NewServer(seedCPURepo(), fake.NewSimpleClientset(), "test-pod")
}

// baseCPUReq is a valid CPU run request; individual tests mutate one field to
// trip a specific guard.
func baseCPUReq() *database.RunRequest {
	return &database.RunRequest{
		ModelHfID: "Qwen/Qwen2.5-1.5B-Instruct", ModelHfRevision: "main",
		InstanceTypeName: "r8g.16xlarge", Framework: "vllm-cpu", FrameworkVersion: "v0.27.0",
		TensorParallelDegree: 1, Concurrency: 16, InputSequenceLength: 512,
		OutputSequenceLength: 256, DatasetName: "sharegpt", RunType: "on_demand", ScenarioID: "chatbot",
	}
}

// assertBadRequest asserts CreateRun returned a 400 createRunError.
func assertBadRequest(t *testing.T, _ string, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("expected a 400 error, got nil")
	}
	var cr *createRunError
	if !errors.As(err, &cr) {
		t.Fatalf("expected *createRunError, got %T: %v", err, err)
	}
	if cr.status != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", cr.status)
	}
}

// TestCreateRun_CPUGuards_FP8: kv_cache_dtype=fp8 on a CPU run → 400.
func TestCreateRun_CPUGuards_FP8(t *testing.T) {
	s := cpuGuardServer()
	req := baseCPUReq()
	req.KVCacheDtype = "fp8"
	_, err := s.CreateRun(context.Background(), req)
	assertBadRequest(t, "", err)
}

// TestCreateRun_CPUGuards_Distributed: a distributed/disaggregated deployment
// mode on a CPU instance → 400.
func TestCreateRun_CPUGuards_Distributed(t *testing.T) {
	for _, mode := range []string{"distributed", "disaggregated"} {
		s := cpuGuardServer()
		req := baseCPUReq()
		req.DeploymentMode = mode
		_, err := s.CreateRun(context.Background(), req)
		assertBadRequest(t, mode, err)
	}
}

// TestCreateRun_CPUGuards_TPExceedsNUMA: TP > NUMA-node count (=1 on
// single-socket Graviton) → 400.
func TestCreateRun_CPUGuards_TPExceedsNUMA(t *testing.T) {
	s := cpuGuardServer()
	req := baseCPUReq()
	req.TensorParallelDegree = 2
	_, err := s.CreateRun(context.Background(), req)
	assertBadRequest(t, "", err)
}

// TestCreateRun_GPUUnaffected: the SAME knobs that trip the CPU guards must still
// pass on a GPU instance (fp8 KV + TP=2 are valid on GPU). This is the mirror
// that proves the guards are CPU-scoped, not global regressions.
func TestCreateRun_GPUUnaffected(t *testing.T) {
	s := cpuGuardServer()
	req := &database.RunRequest{
		ModelHfID: "Qwen/Qwen2.5-1.5B-Instruct", ModelHfRevision: "main",
		InstanceTypeName: "g6.2xlarge", Framework: "vllm", FrameworkVersion: "v0.6.0",
		TensorParallelDegree: 1, KVCacheDtype: "fp8", Concurrency: 16,
		InputSequenceLength: 512, OutputSequenceLength: 256,
		DatasetName: "sharegpt", RunType: "on_demand", ScenarioID: "chatbot",
	}
	// Should NOT be rejected by the CPU guards. (It may still fail later for
	// unrelated reasons in the mock, but not with our CPU-guard 400s.)
	_, err := s.CreateRun(context.Background(), req)
	if err != nil {
		var cr *createRunError
		if errors.As(err, &cr) && cr.status == http.StatusBadRequest {
			// Make sure it's not one of OUR guard messages.
			for _, msg := range []string{"does not apply to CPU", "CPU is single-node", "NUMA-node count", "UNQUANTIZED on CPU"} {
				if strings.Contains(cr.msg, msg) {
					t.Errorf("GPU run wrongly tripped a CPU guard: %s", cr.msg)
				}
			}
		}
	}
}
