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
