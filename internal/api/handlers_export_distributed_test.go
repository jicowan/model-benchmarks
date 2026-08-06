package api

import (
	"context"
	"strings"
	"testing"

	"github.com/accelbench/accelbench/internal/database"
	"github.com/accelbench/accelbench/internal/orchestrator"
	"github.com/accelbench/accelbench/internal/runtime"

	k8sfake "k8s.io/client-go/kubernetes/fake"
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

// TestGenerateManifest_Distributed_LLMDVersion (PRD-66 Part 2): the co-located
// PP export uses the CONFIGURED llm-d-aws tag (RunExportDetails.LLMDVersion),
// not a stale hardcode; an unset version falls back to the shipped default.
func TestGenerateManifest_Distributed_LLMDVersion(t *testing.T) {
	base := func() *database.RunExportDetails {
		return &database.RunExportDetails{
			ModelHfID: "meta-llama/Llama-3.1-70B", InstanceTypeName: "p5.48xlarge",
			Framework: "llm-d", FrameworkVersion: "v0.19.0", // vLLM engine — must NOT tag the image
			TensorParallelDegree: 8, AcceleratorCount: 8, VCPUs: 192, MemoryGiB: 2048,
			DeploymentMode: strptr("distributed"), NodeCount: intptr(2),
			PipelineParallelDegree: intptr(2), NetworkMode: strptr("tcp"),
		}
	}
	// Configured tag flows through.
	d := base()
	d.LLMDVersion = "v0.9.3"
	out, err := generateManifest(d)
	if err != nil {
		t.Fatalf("generateManifest: %v", err)
	}
	if !strings.Contains(out, "ghcr.io/llm-d/llm-d-aws:v0.9.3") {
		t.Error("PP export must use the configured llm-d-aws tag v0.9.3")
	}
	if strings.Contains(out, "llm-d-aws:v0.19.0") {
		t.Error("PP image tag must NOT be the run's vLLM FrameworkVersion")
	}
	// Unset → shipped default pin (byte-identical to pre-PRD-66).
	d2 := base()
	out2, err := generateManifest(d2)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out2, "ghcr.io/llm-d/llm-d-aws:"+runtime.DefaultLLMDVersion) {
		t.Error("PP export with no configured version must fall back to the default pin")
	}
	// PRD-66 Part 2a: with PULL_THROUGH_REGISTRY set, the PP image routes through
	// the GHCR ECR pull-through cache (ghcr prefix), NOT a direct ghcr.io pull.
	d3 := base()
	d3.LLMDVersion = "v0.9.3"
	t.Setenv("PULL_THROUGH_REGISTRY", "820537372947.dkr.ecr.us-east-2.amazonaws.com")
	out3, err := generateManifest(d3)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out3, "820537372947.dkr.ecr.us-east-2.amazonaws.com/ghcr/llm-d/llm-d-aws:v0.9.3") {
		t.Error("PP export must route the configured tag through the GHCR pull-through cache")
	}
	if strings.Contains(out3, "image: ghcr.io/llm-d/llm-d-aws") {
		t.Error("with pull-through set, PP image must NOT be a direct ghcr.io pull")
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

// TestGenerateManifest_Disaggregated_Streamer (PRD-65 Layer 4): a D/P run that
// streamed a cached S3 model exports the streamer serve line (S3 URI +
// runai_streamer + extra-config with memory_limit) AND the
// RUNAI_STREAMER_MEMORY_LIMIT env — so the exported manifest reproduces the
// deployed load. An HF-only D/P run emits neither (byte-identical to pre-PRD-65).
func TestGenerateManifest_Disaggregated_Streamer(t *testing.T) {
	base := func() *database.RunExportDetails {
		return &database.RunExportDetails{
			ModelHfID: "Qwen/Qwen2.5-1.5B-Instruct", InstanceTypeName: "g6.2xlarge",
			Framework: "llm-d", FrameworkVersion: "v0.8.1",
			TensorParallelDegree: 1, AcceleratorCount: 1, VCPUs: 8, MemoryGiB: 32,
			DeploymentMode: strptr("disaggregated"), NodeCount: intptr(2), NetworkMode: strptr("tcp"),
			PrefillReplicas: intptr(1), PrefillTP: intptr(1), DecodeReplicas: intptr(1), DecodeTP: intptr(1),
		}
	}
	// Streamed: UseRunaiStreamer + S3 URI (as resolveExportStreamer would set).
	d := base()
	d.UseRunaiStreamer = true
	s3 := "s3://accelbench-models/qwen"
	d.ModelS3URI = &s3
	out, err := generateManifest(d)
	if err != nil {
		t.Fatalf("generateManifest: %v", err)
	}
	for _, want := range []string{
		"runai_streamer",
		"s3://accelbench-models/qwen",
		"memory_limit",                // extra-config carries it
		"RUNAI_STREAMER_MEMORY_LIMIT", // env on the container
		"17179869184",                 // 32 GiB node / 2 = 16 GiB → bytes
		"accelbench-model",            // S3-access service account
	} {
		if !strings.Contains(out, want) {
			t.Errorf("streamed D/P export missing %q", want)
		}
	}

	// HF-only (no streamer): none of the streamer flags/env appear.
	d2 := base()
	out2, err := generateManifest(d2)
	if err != nil {
		t.Fatal(err)
	}
	for _, notWant := range []string{"runai_streamer", "RUNAI_STREAMER_MEMORY_LIMIT", "s3://"} {
		if strings.Contains(out2, notWant) {
			t.Errorf("HF-only D/P export must NOT contain %q", notWant)
		}
	}
	// HF model id is the serve arg.
	if !strings.Contains(out2, "Qwen/Qwen2.5-1.5B-Instruct") {
		t.Error("HF-only D/P export must use the HF model id")
	}
}

// TestGenerateManifest_Disaggregated_PullThrough: when PULL_THROUGH_REGISTRY is
// set, the exported D/P vLLM image is routed through the Docker Hub ECR
// pull-through cache (matching the deploy path); unset → bare Docker Hub image.
func TestGenerateManifest_Disaggregated_PullThrough(t *testing.T) {
	d := &database.RunExportDetails{
		ModelHfID: "Qwen/Qwen2.5-1.5B-Instruct", InstanceTypeName: "g6.2xlarge",
		Framework: "llm-d", FrameworkVersion: "v0.8.1",
		TensorParallelDegree: 1, AcceleratorCount: 1, VCPUs: 8, MemoryGiB: 32,
		DeploymentMode: strptr("disaggregated"), NodeCount: intptr(2), NetworkMode: strptr("tcp"),
		PrefillReplicas: intptr(1), PrefillTP: intptr(1), DecodeReplicas: intptr(1), DecodeTP: intptr(1),
	}

	t.Setenv("PULL_THROUGH_REGISTRY", "820537372947.dkr.ecr.us-east-2.amazonaws.com")
	out, err := generateManifest(d)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "820537372947.dkr.ecr.us-east-2.amazonaws.com/dockerhub/vllm/vllm-openai:v0.25.0") {
		t.Error("with PULL_THROUGH_REGISTRY set, D/P image must route through the pull-through cache")
	}

	t.Setenv("PULL_THROUGH_REGISTRY", "")
	out2, err := generateManifest(d)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out2, "/dockerhub/vllm/vllm-openai") {
		t.Error("without PULL_THROUGH_REGISTRY, D/P image must be the bare Docker Hub ref")
	}
	if !strings.Contains(out2, "vllm/vllm-openai:v0.25.0") {
		t.Error("D/P export missing the vLLM model image")
	}
}

// TestGenerateManifest_Disaggregated_PDVLLMVersion (PRD-66 Part 2): the D/P
// export composes vllm/vllm-openai from the configured PDVLLMVersion, not a
// stale hardcode; unset ⇒ the shipped default. Also routes through the
// pull-through cache when configured.
func TestGenerateManifest_Disaggregated_PDVLLMVersion(t *testing.T) {
	base := func() *database.RunExportDetails {
		return &database.RunExportDetails{
			ModelHfID: "Qwen/Qwen2.5-1.5B-Instruct", InstanceTypeName: "g6.2xlarge",
			Framework: "llm-d", FrameworkVersion: "v0.19.0",
			TensorParallelDegree: 1, AcceleratorCount: 1, VCPUs: 8, MemoryGiB: 32,
			DeploymentMode: strptr("disaggregated"), NodeCount: intptr(2), NetworkMode: strptr("tcp"),
			PrefillReplicas: intptr(1), PrefillTP: intptr(1), DecodeReplicas: intptr(1), DecodeTP: intptr(1),
		}
	}
	// Configured tag flows through.
	d := base()
	d.PDVLLMVersion = "v0.26.1"
	out, err := generateManifest(d)
	if err != nil {
		t.Fatalf("generateManifest: %v", err)
	}
	if !strings.Contains(out, "vllm/vllm-openai:v0.26.1") {
		t.Error("D/P export must use the configured pd_vllm_version v0.26.1")
	}
	// Configured tag + pull-through cache.
	d2 := base()
	d2.PDVLLMVersion = "v0.26.1"
	t.Setenv("PULL_THROUGH_REGISTRY", "820537372947.dkr.ecr.us-east-2.amazonaws.com")
	out2, err := generateManifest(d2)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out2, "820537372947.dkr.ecr.us-east-2.amazonaws.com/dockerhub/vllm/vllm-openai:v0.26.1") {
		t.Error("D/P export must route the configured tag through the pull-through cache")
	}
	t.Setenv("PULL_THROUGH_REGISTRY", "")
	// Unset version ⇒ default pin.
	d3 := base()
	out3, err := generateManifest(d3)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out3, "vllm/vllm-openai:"+orchestrator.DefaultPDVLLMVersion) {
		t.Error("D/P export with no configured version must fall back to the default pin")
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

// TestResolveExportStreamer (PRD-65 Layer 4): the export handler reproduces the
// D/P cached-model auto-detect (orchestrator resolveS3Model), and NEVER does so
// for PP (distributed) — llm-d-aws can't stream from S3.
func TestResolveExportStreamer(t *testing.T) {
	seed := func() *database.MockRepo {
		repo := database.NewMockRepo()
		hf := "Qwen/Qwen2.5-1.5B-Instruct"
		_, _ = repo.CreateModelCache(context.Background(), &database.ModelCache{
			HfID: &hf, HfRevision: "main", S3URI: "s3://bucket/qwen", Status: "cached",
		})
		return repo
	}

	t.Run("D/P cached model auto-detects", func(t *testing.T) {
		s := NewServer(seed(), k8sfake.NewSimpleClientset(), "test-pod")
		d := &database.RunExportDetails{ModelHfID: "Qwen/Qwen2.5-1.5B-Instruct", DeploymentMode: strptr("disaggregated")}
		s.resolveExportStreamer(context.Background(), d)
		if !d.UseRunaiStreamer || d.ModelS3URI == nil || *d.ModelS3URI != "s3://bucket/qwen" {
			t.Errorf("D/P cached: got useRunai=%v uri=%v, want true + s3://bucket/qwen", d.UseRunaiStreamer, d.ModelS3URI)
		}
	})

	t.Run("PP cached model is NOT auto-detected (guard)", func(t *testing.T) {
		s := NewServer(seed(), k8sfake.NewSimpleClientset(), "test-pod")
		d := &database.RunExportDetails{ModelHfID: "Qwen/Qwen2.5-1.5B-Instruct", DeploymentMode: strptr("distributed")}
		s.resolveExportStreamer(context.Background(), d)
		if d.UseRunaiStreamer || d.ModelS3URI != nil {
			t.Errorf("PP must NOT stream: got useRunai=%v uri=%v", d.UseRunaiStreamer, d.ModelS3URI)
		}
	})

	t.Run("D/P uncached model → no streamer", func(t *testing.T) {
		s := NewServer(database.NewMockRepo(), k8sfake.NewSimpleClientset(), "test-pod")
		d := &database.RunExportDetails{ModelHfID: "org/not-cached", DeploymentMode: strptr("disaggregated")}
		s.resolveExportStreamer(context.Background(), d)
		if d.UseRunaiStreamer || d.ModelS3URI != nil {
			t.Errorf("uncached: got useRunai=%v uri=%v", d.UseRunaiStreamer, d.ModelS3URI)
		}
	})
}
