package manifest

import (
	"strings"
	"testing"
)

func sampleDisaggParams() LLMDDisaggregatedParams {
	return LLMDDisaggregatedParams{
		Name:                "bench-abc12345",
		Namespace:           "accelbench",
		Image:               "vllm/vllm-openai:v0.25.0",
		ServeArgs:           []string{"Qwen/Qwen2.5-1.5B-Instruct", "--trust-remote-code"},
		ContainerName:       "vllm",
		ModelHfID:           "Qwen/Qwen2.5-1.5B-Instruct",
		ModelLabel:          "qwen2-5-1-5b-instruct",
		HfToken:             "hf_test",
		PrefillReplicas:     2,
		PrefillTP:           1,
		DecodeReplicas:      1,
		DecodeTP:            2,
		CPURequest:          "3",
		MemoryRequest:       "12Gi",
		NetworkMode:         "tcp",
		NixlModuleDir:       "/usr/local/lib/python3.12/dist-packages/nixl_cu13.libs/ucx",
		EPPImage:            "ghcr.io/llm-d/llm-d-router-endpoint-picker:v0.9.0",
		SidecarImage:        "ghcr.io/llm-d/llm-d-router-disagg-sidecar:v0.9.0",
		NonCachedTokens:     16,
		GPUDeviceClass:      "gpu.nvidia.com",
		GatewayName:         "accelbench-gateway",
		GatewayNamespace:    "envoy-gateway-system",
		MultiNodeTaintKey:   "accelbench.io/multinode",
		MultiNodeTaintValue: "true",
		DRANodeSelectorKey:  "accelbench.io/dra",
		DRANodeSelectorVal:  "true",
	}
}

func TestRenderLLMDDisaggregated_ObjectGraph(t *testing.T) {
	out, err := RenderLLMDDisaggregated(sampleDisaggParams())
	if err != nil {
		t.Fatalf("RenderLLMDDisaggregated: %v", err)
	}

	// Full object graph present.
	for _, want := range []string{
		"kind: ResourceClaimTemplate",
		"kind: Deployment",
		"kind: Service",
		"kind: InferencePool",
		"kind: ConfigMap",
		"kind: ServiceAccount",
		"kind: Role",
		"kind: RoleBinding",
		"kind: ClusterRole",
		"kind: ClusterRoleBinding",
		"kind: HTTPRoute",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("manifest set missing %q", want)
		}
	}

	// Two role deployments + two role RCTs.
	for _, want := range []string{
		"name: bench-abc12345-prefill\n",
		"name: bench-abc12345-decode\n",
		"name: bench-abc12345-prefill-devices",
		"name: bench-abc12345-decode-devices",
		"name: bench-abc12345-pool",
		"name: bench-abc12345-epp",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing object %q", want)
		}
	}
	// vllm serve appears once per role (prefill + decode).
	if n := strings.Count(out, "vllm"); n < 2 {
		t.Errorf("expected vllm container in both roles")
	}
	// exactly 2 Deployments run the model (prefill+decode) + 1 EPP = 3 total.
	if n := strings.Count(out, "kind: Deployment"); n != 3 {
		t.Errorf("expected 3 Deployments (prefill, decode, EPP), got %d", n)
	}
}

func TestRenderLLMDDisaggregated_PerRoleTopology(t *testing.T) {
	out, err := RenderLLMDDisaggregated(sampleDisaggParams())
	if err != nil {
		t.Fatal(err)
	}
	// Prefill: 2 replicas, TP=1 (GPU claim count 1). Decode: 1 replica, TP=2.
	if !strings.Contains(out, "replicas: 2") {
		t.Error("prefill should have 2 replicas")
	}
	if !strings.Contains(out, "--tensor-parallel-size=1") {
		t.Error("prefill TP=1 flag missing")
	}
	if !strings.Contains(out, "--tensor-parallel-size=2") {
		t.Error("decode TP=2 flag missing")
	}
	// GPU claim counts: prefill count 1, decode count 2.
	if !strings.Contains(out, "count: 1") || !strings.Contains(out, "count: 2") {
		t.Error("per-role DRA GPU counts (1 prefill, 2 decode) missing")
	}
}

func TestRenderLLMDDisaggregated_KVAndSidecar(t *testing.T) {
	out, err := RenderLLMDDisaggregated(sampleDisaggParams())
	if err != nil {
		t.Fatal(err)
	}
	// NIXL KV transfer config on both roles.
	if !strings.Contains(out, `"kv_connector":"NixlConnector","kv_role":"kv_both"`) {
		t.Error("NIXL kv-transfer-config missing")
	}
	// The three live-discovered fixes must be present.
	for _, want := range []string{
		"UCX_TLS",
		`value: "tcp,cuda_copy,cuda_ipc"`, // TCP mode: cuda_copy required for GPU buffers
		"UCX_MODULE_DIR",
		"nixl_cu13.libs/ucx",
		"VLLM_LOGGING_LEVEL",
		`value: "DEBUG"`,
		"VLLM_NIXL_SIDE_CHANNEL_HOST",
		"fieldPath: status.podIP",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing NIXL/KV wiring %q", want)
		}
	}
	// Decode carries the routing sidecar initContainer; prefill does not.
	if !strings.Contains(out, "name: routing-proxy") {
		t.Error("decode should carry the routing-proxy sidecar")
	}
	if n := strings.Count(out, "name: routing-proxy"); n != 1 {
		t.Errorf("exactly one routing-proxy (decode only), got %d", n)
	}
	if !strings.Contains(out, "ghcr.io/llm-d/llm-d-router-disagg-sidecar:v0.9.0") {
		t.Error("sidecar image missing")
	}
}

func TestRenderLLMDDisaggregated_EPPAndRouting(t *testing.T) {
	out, err := RenderLLMDDisaggregated(sampleDisaggParams())
	if err != nil {
		t.Fatal(err)
	}
	// InferencePool spans both roles via the model label selector (role-agnostic).
	if !strings.Contains(out, "llm-d.ai/inference-serving: \"true\"") ||
		!strings.Contains(out, "llm-d.ai/model: qwen2-5-1-5b-instruct") {
		t.Error("InferencePool selector labels missing on pods/pool")
	}
	// EPP config: threshold-gated decider + role filters.
	for _, want := range []string{
		"kind: EndpointPickerConfig",
		"prefix-based-pd-decider",
		"nonCachedTokens: 16",
		"prefill-filter",
		"decode-filter",
		"disagg-profile-handler",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("EPP config missing %q", want)
		}
	}
	// EPP v0.9.0 args + image + health service name.
	for _, want := range []string{
		"ghcr.io/llm-d/llm-d-router-endpoint-picker:v0.9.0",
		"--pool-group",
		"inference.networking.k8s.io",
		"service: inference-extension",
		"appProtocol: http2",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("EPP deployment missing %q", want)
		}
	}
	// HTTPRoute backends the InferencePool (not a Service) + timeout raised.
	if !strings.Contains(out, "kind: InferencePool\n          name: bench-abc12345-pool") {
		t.Error("HTTPRoute should backendRef the InferencePool")
	}
	if !strings.Contains(out, `request: "3600s"`) {
		t.Error("HTTPRoute must raise the request timeout")
	}
}

func TestRenderLLMDDisaggregated_EFAMode(t *testing.T) {
	p := sampleDisaggParams()
	p.NetworkMode = "efa"
	out, err := RenderLLMDDisaggregated(p)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `value: "efa"`) || !strings.Contains(out, "FI_PROVIDER") {
		t.Error("EFA mode should set FI_PROVIDER=efa")
	}
	if strings.Contains(out, `value: "tcp,cuda_copy,cuda_ipc"`) {
		t.Error("EFA mode should not use the TCP UCX_TLS value")
	}
}

func TestRenderLLMDDisaggregated_SeparateNodes(t *testing.T) {
	out, err := RenderLLMDDisaggregated(sampleDisaggParams())
	if err != nil {
		t.Fatal(err)
	}
	// Both roles pin podAntiAffinity against the other role (KV crosses the net).
	if n := strings.Count(out, "podAntiAffinity"); n != 2 {
		t.Errorf("both roles need podAntiAffinity for separate nodes, got %d", n)
	}
}

// TestRenderLLMDDisaggregated_PerRoleSchedulerOverride (PRD-64): when per-role
// ServeArgs are set, the prefill and decode Deployments emit DIFFERENT
// --max-num-batched-tokens, while model-identity flags stay identical.
func TestRenderLLMDDisaggregated_PerRoleSchedulerOverride(t *testing.T) {
	p := sampleDisaggParams()
	// Shared/default args (what BuildArgs would emit) + per-role overrides that
	// differ only in --max-num-batched-tokens.
	base := []string{"Qwen/Qwen2.5-1.5B-Instruct", "--trust-remote-code", "--max-model-len", "4096"}
	p.ServeArgs = append(append([]string{}, base...), "--max-num-batched-tokens", "2048")
	p.PrefillServeArgs = append(append([]string{}, base...), "--max-num-batched-tokens", "16384")
	p.DecodeServeArgs = append(append([]string{}, base...), "--max-num-batched-tokens", "2048")

	out, err := RenderLLMDDisaggregated(p)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"--max-num-batched-tokens"`) {
		t.Fatal("expected the flag rendered")
	}
	// Both per-role values must appear (16384 prefill, 2048 decode).
	if !strings.Contains(out, `"16384"`) || !strings.Contains(out, `"2048"`) {
		t.Errorf("expected both per-role batched-token values (16384 prefill, 2048 decode)")
	}
	// Model-identity flag identical across roles → appears for both (2x).
	if n := strings.Count(out, `"--max-model-len"`); n != 2 {
		t.Errorf("--max-model-len should appear once per role (2), got %d", n)
	}
}

// Regression: with NO per-role override, the render falls back to the shared
// ServeArgs for both roles — byte-identical to pre-PRD-64 output.
func TestRenderLLMDDisaggregated_NoOverrideFallsBackToShared(t *testing.T) {
	shared := sampleDisaggParams()
	shared.ServeArgs = []string{"Qwen/Qwen2.5-1.5B-Instruct", "--trust-remote-code", "--max-num-batched-tokens", "2048"}
	// PrefillServeArgs / DecodeServeArgs left nil.
	withNil, err := RenderLLMDDisaggregated(shared)
	if err != nil {
		t.Fatal(err)
	}
	// Both roles get 2048; no other batched-token value present.
	if n := strings.Count(withNil, `"--max-num-batched-tokens"`); n != 2 {
		t.Errorf("shared arg should render for both roles (2), got %d", n)
	}
	if strings.Contains(withNil, `"16384"`) {
		t.Error("no per-role override set → no divergent value should appear")
	}
}
