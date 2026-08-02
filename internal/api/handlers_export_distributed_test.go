package api

import (
	"strings"
	"testing"

	"github.com/accelbench/accelbench/internal/database"
)

func strptr(s string) *string { return &s }
func intptr(i int) *int       { return &i }

// TestGenerateManifest_Distributed: a co-located multi-node run exports the
// llm-d LeaderWorkerSet graph, not a single-node Deployment (PRD-59 fix).
func TestGenerateManifest_Distributed(t *testing.T) {
	d := &database.RunExportDetails{
		ModelHfID: "meta-llama/Llama-3.1-70B", InstanceTypeName: "p5.48xlarge",
		Framework: "llm-d", FrameworkVersion: "v0.8.1",
		TensorParallelDegree: 8, AcceleratorCount: 8, VCPUs: 192, MemoryGiB: 2048,
		DeploymentMode: strptr("distributed"), NodeCount: intptr(2),
		PipelineParallelDegree: intptr(2), NetworkMode: strptr("tcp"),
	}
	out, err := generateManifest(d)
	if err != nil {
		t.Fatalf("generateManifest: %v", err)
	}
	if !strings.Contains(out, "kind: LeaderWorkerSet") {
		t.Error("distributed export must render a LeaderWorkerSet")
	}
	if strings.Contains(out, "kind: InferencePool") {
		t.Error("co-located distributed export should NOT include an InferencePool (PRD-56 dropped it)")
	}
	// TCP mode: no EFA claim.
	if strings.Contains(out, "efa.networking.k8s.aws") {
		t.Error("tcp-mode export must not claim EFA")
	}
}

// TestGenerateManifest_Distributed_ReproducesAppliedConfig (PP, user-supplied
// values): the exported LeaderWorkerSet must reproduce the run's actual topology
// (node_count/TP/PP) AND the user's vLLM knob overrides (max-model-len,
// max-num-batched-tokens, kv-cache-dtype) — not defaults.
func TestGenerateManifest_Distributed_ReproducesAppliedConfig(t *testing.T) {
	mnbt := 24576
	kvd := "fp8"
	d := &database.RunExportDetails{
		ModelHfID: "meta-llama/Llama-3.1-70B", InstanceTypeName: "p5.48xlarge",
		Framework: "llm-d", FrameworkVersion: "v0.8.1",
		TensorParallelDegree: 4, AcceleratorCount: 8, VCPUs: 192, MemoryGiB: 2048,
		MaxModelLen:         8192,
		MaxNumBatchedTokens: &mnbt,
		KVCacheDtype:        &kvd,
		DeploymentMode:      strptr("distributed"), NodeCount: intptr(3),
		PipelineParallelDegree: intptr(3), NetworkMode: strptr("efa"),
	}
	out, err := generateManifest(d)
	if err != nil {
		t.Fatalf("generateManifest: %v", err)
	}
	// Topology as applied: LWS group size 3, TP=4, PP=3.
	for _, want := range []string{
		"kind: LeaderWorkerSet",
		"size: 3",          // NodeCount → LWS group size
		"TP_SIZE=4",        // user TP (not forced to fill the 8-GPU node)
		"PP_SIZE=3",        // user PP
	} {
		if !strings.Contains(out, want) {
			t.Errorf("distributed export missing applied topology %q", want)
		}
	}
	// User vLLM knob overrides flow through the ServeArgs.
	for _, want := range []string{
		"--max-model-len", "8192",
		"--max-num-batched-tokens", "24576",
		"--kv-cache-dtype", "fp8",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("distributed export missing applied knob %q", want)
		}
	}
	// EFA mode: the EFA device class IS claimed (opposite of the tcp test).
	if !strings.Contains(out, "efa.networking.k8s.aws") {
		t.Error("efa-mode distributed export must claim EFA")
	}
}

// TestGenerateManifest_Disaggregated: a PD run exports the two-group +
// InferencePool + EPP graph.
func TestGenerateManifest_Disaggregated(t *testing.T) {
	d := &database.RunExportDetails{
		ModelHfID: "Qwen/Qwen2.5-1.5B-Instruct", InstanceTypeName: "g6.2xlarge",
		Framework: "llm-d", FrameworkVersion: "v0.8.1",
		TensorParallelDegree: 1, AcceleratorCount: 1, VCPUs: 8, MemoryGiB: 32,
		DeploymentMode: strptr("disaggregated"), NodeCount: intptr(2), NetworkMode: strptr("tcp"),
		PrefillReplicas: intptr(1), PrefillTP: intptr(1), DecodeReplicas: intptr(1), DecodeTP: intptr(1),
	}
	out, err := generateManifest(d)
	if err != nil {
		t.Fatalf("generateManifest: %v", err)
	}
	for _, want := range []string{"kind: InferencePool", "kind: Deployment", "routing-proxy", "EndpointPickerConfig"} {
		if !strings.Contains(out, want) {
			t.Errorf("disaggregated export missing %q", want)
		}
	}
	// Resource names must be DNS-1123 safe — the model id "Qwen/Qwen2.5-1.5B"
	// has a dot that k8s object names reject; sanitizeDNS1123 must strip it.
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "name:") && strings.Contains(line, "pd-") {
			if strings.Contains(line, ".") {
				t.Errorf("resource name contains a dot (invalid k8s name): %q", strings.TrimSpace(line))
			}
		}
	}
}

// TestGenerateManifest_SingleNode: a normal single-instance run still exports
// the plain vLLM Deployment (unchanged behavior — no deployment_mode).
func TestGenerateManifest_SingleNode(t *testing.T) {
	d := &database.RunExportDetails{
		ModelHfID: "meta-llama/Llama-3.1-8B", InstanceTypeName: "g5.xlarge",
		Framework: "vllm", FrameworkVersion: "v0.6.0",
		TensorParallelDegree: 1, AcceleratorType: "gpu", AcceleratorCount: 1, VCPUs: 4, MemoryGiB: 16,
		// DeploymentMode nil → single-node path.
	}
	out, err := generateManifest(d)
	if err != nil {
		t.Fatalf("generateManifest: %v", err)
	}
	if strings.Contains(out, "kind: LeaderWorkerSet") || strings.Contains(out, "kind: InferencePool") {
		t.Error("single-node export must be a plain Deployment, not an llm-d graph")
	}
	if !strings.Contains(out, "kind: Deployment") {
		t.Error("single-node export must render a Deployment")
	}
}
