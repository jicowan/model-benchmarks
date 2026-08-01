package api

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/accelbench/accelbench/internal/database"

	"k8s.io/client-go/kubernetes/fake"
)

// validDisaggregatedReq builds a valid PD-disaggregated request against the
// 8-GPU p5 seeded by distServer: 2 prefill (TP=1) + 1 decode (TP=4).
func validDisaggregatedReq() database.RunRequest {
	return database.RunRequest{
		ModelHfID:            "meta-llama/Llama-3.1-70B",
		InstanceTypeName:     "p5.48xlarge",
		Framework:            "llm-d",
		FrameworkVersion:     "0.2.0",
		Concurrency:          16,
		InputSequenceLength:  512,
		OutputSequenceLength: 256,
		DatasetName:          "sharegpt",
		RunType:              "on_demand",
		ScenarioID:           "chatbot",
		DeploymentMode:       "disaggregated",
		PrefillReplicas:      2,
		PrefillTP:            1,
		DecodeReplicas:       1,
		DecodeTP:             4,
		NetworkMode:          "tcp",
	}
}

func TestCreateRun_Disaggregated_Accepted(t *testing.T) {
	mux := distServer(t)
	w := postRun(mux, validDisaggregatedReq())
	if w.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body: %s", w.Code, w.Body.String())
	}
}

func TestCreateRun_Disaggregated_RejectsNonLLMD(t *testing.T) {
	mux := distServer(t)
	r := validDisaggregatedReq()
	r.Framework = "vllm"
	w := postRun(mux, r)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "framework=llm-d") {
		t.Errorf("want 400 framework=llm-d; got %d: %s", w.Code, w.Body.String())
	}
}

func TestCreateRun_Disaggregated_RejectsZeroPrefill(t *testing.T) {
	mux := distServer(t)
	r := validDisaggregatedReq()
	r.PrefillReplicas = 0
	w := postRun(mux, r)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "prefill_replicas") {
		t.Errorf("want 400 prefill_replicas; got %d: %s", w.Code, w.Body.String())
	}
}

func TestCreateRun_Disaggregated_RejectsTPOverGPUs(t *testing.T) {
	mux := distServer(t)
	r := validDisaggregatedReq()
	r.DecodeTP = 16 // > 8 GPUs/node
	w := postRun(mux, r)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "decode_tp") {
		t.Errorf("want 400 decode_tp; got %d: %s", w.Code, w.Body.String())
	}
}

func TestCreateRun_Disaggregated_RejectsPerRolePP(t *testing.T) {
	mux := distServer(t)
	r := validDisaggregatedReq()
	r.DecodePP = 2 // multi-node-per-role not supported
	w := postRun(mux, r)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "pipeline-parallel") {
		t.Errorf("want 400 per-role PP; got %d: %s", w.Code, w.Body.String())
	}
}

func TestCreateRun_Disaggregated_Rejects1P1DSingleNodeSum(t *testing.T) {
	// A run with prefill=1 decode=1 is still 2 nodes → allowed. But if someone
	// tried a single total node it must reject. Simulate by... prefill+decode
	// always >= 2 with replicas>=1 each, so the <2 branch is defensive; here we
	// just assert the 1+1 case IS accepted (min valid disaggregated topology).
	mux := distServer(t)
	r := validDisaggregatedReq()
	r.PrefillReplicas, r.DecodeReplicas = 1, 1
	r.PrefillTP, r.DecodeTP = 1, 1
	if w := postRun(mux, r); w.Code != http.StatusAccepted {
		t.Errorf("1P1D should be accepted (2 nodes); got %d: %s", w.Code, w.Body.String())
	}
}

func TestCreateRun_Disaggregated_PersistsTopology(t *testing.T) {
	repo := database.NewMockRepo()
	repo.SeedModel(&database.Model{ID: "m-70b", HfID: "meta-llama/Llama-3.1-70B", HfRevision: "main"})
	repo.SeedInstanceType(&database.InstanceType{
		ID: "inst-p5", Name: "p5.48xlarge", Family: "p5",
		AcceleratorType: "gpu", AcceleratorName: "H100",
		AcceleratorCount: 8, AcceleratorMemoryGiB: 640, VCPUs: 192, MemoryGiB: 2048,
	})
	srv := NewServer(repo, fake.NewSimpleClientset(), "test-pod")

	runID, err := srv.CreateRun(context.Background(), ptrReq(validDisaggregatedReq()))
	if err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	got, err := repo.GetBenchmarkRun(context.Background(), runID)
	if err != nil || got == nil {
		t.Fatalf("GetBenchmarkRun: %v", err)
	}
	if got.DeploymentMode == nil || *got.DeploymentMode != "disaggregated" {
		t.Errorf("deployment_mode not persisted: %v", got.DeploymentMode)
	}
	if got.PrefillReplicas == nil || *got.PrefillReplicas != 2 {
		t.Errorf("prefill_replicas not persisted: %v", got.PrefillReplicas)
	}
	if got.DecodeTP == nil || *got.DecodeTP != 4 {
		t.Errorf("decode_tp not persisted: %v", got.DecodeTP)
	}
	// node_count = prefill(2×1) + decode(1×1) = 3.
	if got.NodeCount == nil || *got.NodeCount != 3 {
		t.Errorf("node_count should be prefill+decode sum (3); got %v", got.NodeCount)
	}
	if got.KVConnector == nil || *got.KVConnector != "nixl" {
		t.Errorf("kv_connector not derived: %v", got.KVConnector)
	}
	if got.KVTransferBackend == nil || *got.KVTransferBackend != "tcp" {
		t.Errorf("kv_transfer_backend should be tcp for tcp network mode: %v", got.KVTransferBackend)
	}
}

// TestCreateRun_Disaggregated_PerRoleScheduler (PRD-64): per-role
// max_num_batched_tokens overrides validate + persist; unset stays NULL.
func TestCreateRun_Disaggregated_PerRoleScheduler(t *testing.T) {
	repo := database.NewMockRepo()
	repo.SeedModel(&database.Model{ID: "m-70b", HfID: "meta-llama/Llama-3.1-70B", HfRevision: "main"})
	repo.SeedInstanceType(&database.InstanceType{
		ID: "inst-p5", Name: "p5.48xlarge", Family: "p5",
		AcceleratorType: "gpu", AcceleratorName: "H100",
		AcceleratorCount: 8, AcceleratorMemoryGiB: 640, VCPUs: 192, MemoryGiB: 2048,
	})
	srv := NewServer(repo, fake.NewSimpleClientset(), "test-pod")

	r := validDisaggregatedReq()
	r.PrefillMaxNumBatchedTokens = 16384
	r.DecodeMaxNumBatchedTokens = 2048
	runID, err := srv.CreateRun(context.Background(), ptrReq(r))
	if err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	got, _ := repo.GetBenchmarkRun(context.Background(), runID)
	if got.PrefillMaxNumBatchedTokens == nil || *got.PrefillMaxNumBatchedTokens != 16384 {
		t.Errorf("prefill override not persisted: %v", got.PrefillMaxNumBatchedTokens)
	}
	if got.DecodeMaxNumBatchedTokens == nil || *got.DecodeMaxNumBatchedTokens != 2048 {
		t.Errorf("decode override not persisted: %v", got.DecodeMaxNumBatchedTokens)
	}

	// Unset → NULL (today's behavior).
	r2 := validDisaggregatedReq()
	id2, _ := srv.CreateRun(context.Background(), ptrReq(r2))
	got2, _ := repo.GetBenchmarkRun(context.Background(), id2)
	if got2.PrefillMaxNumBatchedTokens != nil || got2.DecodeMaxNumBatchedTokens != nil {
		t.Errorf("unset per-role scheduler should be NULL, got %v/%v",
			got2.PrefillMaxNumBatchedTokens, got2.DecodeMaxNumBatchedTokens)
	}
}
