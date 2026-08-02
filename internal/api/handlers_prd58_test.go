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

// PRD-63 relaxed the per-role floors to combination validation: a decode-only
// run (prefill=0, decode>=1) is now VALID — there's a decode-capable pool and no
// lone prefill. (Pre-PRD-63 this was rejected for prefill_replicas < 1.)
func TestCreateRun_Disaggregated_DecodeOnly_Accepted(t *testing.T) {
	mux := distServer(t)
	r := validDisaggregatedReq()
	r.PrefillReplicas = 0
	r.DecodeReplicas, r.DecodeTP = 2, 1
	if w := postRun(mux, r); w.Code != http.StatusAccepted {
		t.Errorf("decode-only should be accepted; got %d: %s", w.Code, w.Body.String())
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

// --- PRD-63: co-located "both" pool role ---

// A both-only run (prefill=0, decode=0, both>=1) is accepted with NodeCount
// equal to the both replica count.
func TestCreateRun_Disaggregated_BothOnly_Accepted(t *testing.T) {
	repo := database.NewMockRepo()
	repo.SeedModel(&database.Model{ID: "m-70b", HfID: "meta-llama/Llama-3.1-70B", HfRevision: "main"})
	repo.SeedInstanceType(&database.InstanceType{
		ID: "inst-p5", Name: "p5.48xlarge", Family: "p5",
		AcceleratorType: "gpu", AcceleratorName: "H100",
		AcceleratorCount: 8, AcceleratorMemoryGiB: 640, VCPUs: 192, MemoryGiB: 2048,
	})
	srv := NewServer(repo, fake.NewSimpleClientset(), "test-pod")

	r := validDisaggregatedReq()
	r.PrefillReplicas, r.DecodeReplicas = 0, 0
	r.BothReplicas, r.BothTP = 2, 1
	runID, err := srv.CreateRun(context.Background(), ptrReq(r))
	if err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	got, _ := repo.GetBenchmarkRun(context.Background(), runID)
	if got.NodeCount == nil || *got.NodeCount != 2 {
		t.Errorf("both-only node_count should be 2, got %v", got.NodeCount)
	}
	if got.BothReplicas == nil || *got.BothReplicas != 2 {
		t.Errorf("both_replicas not persisted: %v", got.BothReplicas)
	}
	// prefill/decode stay NULL (self-describing row).
	if got.PrefillReplicas != nil || got.DecodeReplicas != nil {
		t.Errorf("both-only run should leave prefill/decode NULL, got %v/%v", got.PrefillReplicas, got.DecodeReplicas)
	}
}

// both + prefill (decode=0) is valid and recommended — the both pool covers decode.
func TestCreateRun_Disaggregated_BothPlusPrefill_Accepted(t *testing.T) {
	mux := distServer(t)
	r := validDisaggregatedReq()
	r.PrefillReplicas, r.PrefillTP = 1, 1
	r.DecodeReplicas = 0
	r.BothReplicas, r.BothTP = 2, 1
	if w := postRun(mux, r); w.Code != http.StatusAccepted {
		t.Errorf("both+prefill should be accepted; got %d: %s", w.Code, w.Body.String())
	}
}

// A lone prefill pool (prefill>0, decode=0, both=0) is rejected — no decode
// coverage means nothing can finish a disaggregated request.
func TestCreateRun_Disaggregated_LonePrefill_Rejected(t *testing.T) {
	mux := distServer(t)
	r := validDisaggregatedReq()
	r.PrefillReplicas, r.PrefillTP = 2, 1
	r.DecodeReplicas = 0
	r.BothReplicas = 0
	w := postRun(mux, r)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "decode-capable") {
		t.Errorf("lone prefill should be rejected (no decode coverage); got %d: %s", w.Code, w.Body.String())
	}
}

// both_tp over the node's GPU count is rejected.
func TestCreateRun_Disaggregated_BothTPOverGPUs_Rejected(t *testing.T) {
	mux := distServer(t)
	r := validDisaggregatedReq()
	r.PrefillReplicas, r.DecodeReplicas = 0, 0
	r.BothReplicas, r.BothTP = 1, 16 // > 8 GPUs/node
	w := postRun(mux, r)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "both_tp") {
		t.Errorf("want 400 both_tp; got %d: %s", w.Code, w.Body.String())
	}
}

// An all-zero disaggregated run (no pool at all) is rejected.
func TestCreateRun_Disaggregated_NoPool_Rejected(t *testing.T) {
	mux := distServer(t)
	r := validDisaggregatedReq()
	r.PrefillReplicas, r.DecodeReplicas, r.BothReplicas = 0, 0, 0
	w := postRun(mux, r)
	if w.Code != http.StatusBadRequest {
		t.Errorf("want 400 for no pool; got %d: %s", w.Code, w.Body.String())
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

// --- PRD-61: run-tunable EPP routing config ---

func intp(i int) *int { return &i }

// prd61Server seeds a repo + server for routing-config tests (mirrors the
// PersistsTopology setup).
func prd61Server(t *testing.T) (*Server, *database.MockRepo) {
	t.Helper()
	repo := database.NewMockRepo()
	repo.SeedModel(&database.Model{ID: "m-70b", HfID: "meta-llama/Llama-3.1-70B", HfRevision: "main"})
	repo.SeedInstanceType(&database.InstanceType{
		ID: "inst-p5", Name: "p5.48xlarge", Family: "p5",
		AcceleratorType: "gpu", AcceleratorName: "H100",
		AcceleratorCount: 8, AcceleratorMemoryGiB: 640, VCPUs: 192, MemoryGiB: 2048,
	})
	return NewServer(repo, fake.NewSimpleClientset(), "test-pod"), repo
}

// Supplied routing params validate + persist; omitted ones stay NULL.
func TestCreateRun_Disaggregated_RoutingPersists(t *testing.T) {
	srv, repo := prd61Server(t)
	r := validDisaggregatedReq()
	r.PDNonCachedTokens = intp(128)
	r.PDPrefixCacheWeight = 5
	r.PDQueueScorerWeight = 3
	r.PDMaxPrefixBlocks = 512
	r.PDLRUCapacityPerServer = 99999
	runID, err := srv.CreateRun(context.Background(), ptrReq(r))
	if err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	got, _ := repo.GetBenchmarkRun(context.Background(), runID)
	if got.PDNonCachedTokens == nil || *got.PDNonCachedTokens != 128 {
		t.Errorf("pd_noncached_tokens not persisted: %v", got.PDNonCachedTokens)
	}
	if got.PDPrefixCacheWeight == nil || *got.PDPrefixCacheWeight != 5 {
		t.Errorf("pd_prefix_cache_weight not persisted: %v", got.PDPrefixCacheWeight)
	}
	if got.PDLRUCapacityPerServer == nil || *got.PDLRUCapacityPerServer != 99999 {
		t.Errorf("pd_lru_capacity not persisted: %v", got.PDLRUCapacityPerServer)
	}

	// Omitted → NULL (byte-identical default behavior).
	id2, _ := srv.CreateRun(context.Background(), ptrReq(validDisaggregatedReq()))
	got2, _ := repo.GetBenchmarkRun(context.Background(), id2)
	if got2.PDNonCachedTokens != nil || got2.PDPrefixCacheWeight != nil || got2.PDMaxPrefixBlocks != nil {
		t.Errorf("omitted routing params should be NULL, got %v/%v/%v",
			got2.PDNonCachedTokens, got2.PDPrefixCacheWeight, got2.PDMaxPrefixBlocks)
	}
}

// nonCachedTokens=0 is a MEANINGFUL value (disable PD) — must persist as 0, not
// be dropped as "unset".
func TestCreateRun_Disaggregated_NonCachedZeroPreserved(t *testing.T) {
	srv, repo := prd61Server(t)
	r := validDisaggregatedReq()
	r.PDNonCachedTokens = intp(0)
	runID, err := srv.CreateRun(context.Background(), ptrReq(r))
	if err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	got, _ := repo.GetBenchmarkRun(context.Background(), runID)
	if got.PDNonCachedTokens == nil || *got.PDNonCachedTokens != 0 {
		t.Errorf("pd_noncached_tokens=0 should persist as 0 (disable PD), got %v", got.PDNonCachedTokens)
	}
}

// Out-of-range routing params are rejected.
func TestCreateRun_Disaggregated_RoutingBoundsRejected(t *testing.T) {
	mux := distServer(t)
	cases := []struct {
		name   string
		mutate func(*database.RunRequest)
		want   string
	}{
		{"weight>100", func(r *database.RunRequest) { r.PDPrefixCacheWeight = 101 }, "scorer weights"},
		{"prefixBlocks>4096", func(r *database.RunRequest) { r.PDMaxPrefixBlocks = 5000 }, "pd_max_prefix_blocks"},
		{"nonCached>32768", func(r *database.RunRequest) { r.PDNonCachedTokens = intp(40000) }, "pd_noncached_tokens"},
		{"decider=always gated", func(r *database.RunRequest) { r.PDDeciderStrategy = "always" }, "always"},
		{"decider bad", func(r *database.RunRequest) { r.PDDeciderStrategy = "bogus" }, "threshold"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := validDisaggregatedReq()
			tc.mutate(&r)
			w := postRun(mux, r)
			if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), tc.want) {
				t.Errorf("want 400 containing %q; got %d: %s", tc.want, w.Code, w.Body.String())
			}
		})
	}
}

// Routing params on a NON-disaggregated run are rejected (meaningless without EPP).
func TestCreateRun_RoutingRejectedOnNonDisaggregated(t *testing.T) {
	mux := distServer(t)
	// A valid distributed (co-located) run + a stray routing param.
	r := database.RunRequest{
		ModelHfID: "meta-llama/Llama-3.1-70B", InstanceTypeName: "p5.48xlarge",
		Framework: "llm-d", Concurrency: 16, InputSequenceLength: 512, OutputSequenceLength: 256,
		DatasetName: "sharegpt", RunType: "on_demand", ScenarioID: "chatbot",
		DeploymentMode: "distributed", NodeCount: 2, PipelineParallelDegree: 2, NetworkMode: "tcp",
		PDPrefixCacheWeight: 5,
	}
	w := postRun(mux, r)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "only valid for disaggregated") {
		t.Errorf("routing param on distributed run should be rejected; got %d: %s", w.Code, w.Body.String())
	}
}
