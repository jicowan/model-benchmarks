package manifest

import (
	"strings"
	"testing"
)

// TestRenderLLMDDisaggregated_StreamerMemoryLimit (PRD-65 Layer 3): the
// RUNAI_STREAMER_MEMORY_LIMIT env is emitted (in bytes) on the model containers
// when StreamerMemoryLimitGiB > 0, and omitted otherwise (byte-identical to
// pre-PRD-65 for an HF run).
func TestRenderLLMDDisaggregated_StreamerMemoryLimit(t *testing.T) {
	// Default (0) → no env.
	out, err := RenderLLMDDisaggregated(sampleDisaggParams())
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if strings.Contains(out, "RUNAI_STREAMER_MEMORY_LIMIT") {
		t.Error("no memory-limit env expected when StreamerMemoryLimitGiB == 0")
	}

	// Set → env present in bytes (16 GiB = 17179869184).
	p := sampleDisaggParams()
	p.StreamerMemoryLimitGiB = 16
	out, err = RenderLLMDDisaggregated(p)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(out, "RUNAI_STREAMER_MEMORY_LIMIT") {
		t.Error("memory-limit env expected when StreamerMemoryLimitGiB > 0")
	}
	if !strings.Contains(out, "17179869184") {
		t.Errorf("memory-limit should render as bytes (16 GiB = 17179869184)")
	}
}

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
		// PRD-61: routing knobs at their shipped defaults (what the orchestrator
		// passes when a run supplies no overrides).
		PrefixCacheScorerWeight: 2,
		QueueScorerWeight:       1,
		MaxPrefixBlocksToMatch:  256,
		LRUCapacityPerServer:    31250,
		GPUDeviceClass:      "gpu.nvidia.com",
		GatewayName:         "accelbench-gateway",
		GatewayNamespace:    "envoy-gateway-system",
		MultiNodeTaintKey:   "accelbench.io/multinode",
		MultiNodeTaintValue: "true",
		DRANodeSelectorKey:  "accelbench.io/dra",
		DRANodeSelectorVal:  "true",
		InstanceTypeName:    "g6.48xlarge",
	}
}

// TestRenderLLMDDisaggregated_InstanceTypePinned: every serving pod pins the
// run's selected instance type so the AZ pool (which provisions from the GPU
// instance-category) lands the right hardware.
func TestRenderLLMDDisaggregated_InstanceTypePinned(t *testing.T) {
	out, err := RenderLLMDDisaggregated(sampleDisaggParams())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "node.kubernetes.io/instance-type: g6.48xlarge") {
		t.Error("serving pods must pin the run's instance type")
	}
	// prefill + decode each pin it (2 role pods in the default sample).
	if n := strings.Count(out, "node.kubernetes.io/instance-type: g6.48xlarge"); n != 2 {
		t.Errorf("expected instance-type selector on both role pods (2), got %d", n)
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
	// enable_cross_layers_blocks (AWS+llm-d reference) reduces KV bytes moved
	// prefill→decode; default-on for all disaggregated runs, transport-agnostic.
	if !strings.Contains(out, `"kv_connector_extra_config":{"enable_cross_layers_blocks":"True"}`) {
		t.Error("kv_connector_extra_config with enable_cross_layers_blocks missing")
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
	// The CPU-only EPP must schedule on the general-purpose (system) Karpenter
	// pool like the loadgen: tolerate the dedicated taint + select node-type=system.
	// Otherwise it can't tolerate that pool's taint and stalls Pending when the
	// managed system nodegroup is full.
	for _, want := range []string{
		"key: accelbench.io/dedicated",       // tolerate the general pool's taint
		"key: karpenter.sh/nodepool",         // pin to the Karpenter general pool (excludes the MNG)
		"- general-purpose",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("EPP pod must target the Karpenter general pool; missing %q", want)
		}
	}
	// No EPPZone set in the sample → no zone constraint on the EPP.
	if strings.Contains(out, "topology.kubernetes.io/zone") {
		t.Error("EPP should have no zone constraint when EPPZone is empty")
	}
}

// TestRenderLLMDDisaggregated_EPPZone: when EPPZone is set, the EPP gets a
// topology.kubernetes.io/zone nodeSelector to co-locate it with the serving AZ.
func TestRenderLLMDDisaggregated_EPPZone(t *testing.T) {
	p := sampleDisaggParams()
	p.EPPZone = "us-east-2a"
	out, err := RenderLLMDDisaggregated(p)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "topology.kubernetes.io/zone") || !strings.Contains(out, "- us-east-2a") {
		t.Error("EPP should carry a zone nodeSelector for EPPZone=us-east-2a")
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

// TestRenderLLMDDisaggregated_BothRole (PRD-63): a run with BothReplicas > 0
// renders a {{.Name}}-both Deployment with role=both, the routing sidecar, the
// NIXL kv-transfer config, the both TP flag, a -both-devices claim, and the pool
// selector labels — alongside the prefill/decode graph.
func TestRenderLLMDDisaggregated_BothRole(t *testing.T) {
	p := sampleDisaggParams()
	p.BothReplicas = 3
	p.BothTP = 2
	out, err := RenderLLMDDisaggregated(p)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"name: bench-abc12345-both\n",
		"name: bench-abc12345-both-devices",
		// PRD-63: the wire role value is the canonical "prefill-decode", not the
		// deprecated "both" alias (object names still use -both).
		"llm-d.ai/role: prefill-decode",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("both role missing %q", want)
		}
	}
	// The deprecated bare "both" role value must NOT be emitted.
	if strings.Contains(out, "llm-d.ai/role: both\n") {
		t.Error("should render canonical prefill-decode, not the deprecated both alias")
	}
	// 4 Deployments now (prefill, decode, both, EPP).
	if n := strings.Count(out, "kind: Deployment"); n != 4 {
		t.Errorf("expected 4 Deployments (prefill, decode, both, EPP), got %d", n)
	}
	// both carries the sidecar too → 2 routing-proxy init containers (decode + both).
	if n := strings.Count(out, "name: routing-proxy"); n != 2 {
		t.Errorf("expected routing-proxy on decode AND both (2), got %d", n)
	}
	// both TP=2 flag present; both DRA claim count 2.
	if !strings.Contains(out, "--tensor-parallel-size=2") {
		t.Error("both TP=2 flag missing")
	}
	// both pod's vLLM behind the sidecar → --port=8200 (like decode).
	if n := strings.Count(out, "--port=8200"); n < 2 {
		t.Errorf("both + decode should both serve vLLM on 8200, got %d", n)
	}
	// pool selector labels on the both pod.
	if !strings.Contains(out, "llm-d.ai/inference-serving: \"true\"") {
		t.Error("both pod missing pool selector label")
	}
}

// TestRenderLLMDDisaggregated_BothOnly (PRD-63): a both-only run (prefill=0,
// decode=0) renders ONLY the both role — no prefill/decode Deployment, RCT, or
// Service — plus the shared InferencePool/EPP/HTTPRoute.
func TestRenderLLMDDisaggregated_BothOnly(t *testing.T) {
	p := sampleDisaggParams()
	p.PrefillReplicas = 0
	p.DecodeReplicas = 0
	p.BothReplicas = 2
	p.BothTP = 1
	out, err := RenderLLMDDisaggregated(p)
	if err != nil {
		t.Fatal(err)
	}
	// No prefill/decode role objects. (Use newline-terminated role values so
	// "llm-d.ai/role: prefill\n" doesn't spuriously match the both pod's
	// "llm-d.ai/role: prefill-decode\n".)
	for _, notWant := range []string{
		"name: bench-abc12345-prefill\n",
		"name: bench-abc12345-decode\n",
		"name: bench-abc12345-prefill-devices",
		"name: bench-abc12345-decode-devices",
		"llm-d.ai/role: prefill\n",
		"llm-d.ai/role: decode\n",
	} {
		if strings.Contains(out, notWant) {
			t.Errorf("both-only run should NOT render %q", notWant)
		}
	}
	// Exactly 2 Deployments: both + EPP.
	if n := strings.Count(out, "kind: Deployment"); n != 2 {
		t.Errorf("both-only: expected 2 Deployments (both, EPP), got %d", n)
	}
	// The both role + shared routing graph are present.
	for _, want := range []string{
		"name: bench-abc12345-both\n",
		"name: bench-abc12345-both-devices",
		"kind: InferencePool",
		"kind: HTTPRoute",
		"name: bench-abc12345-pool",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("both-only run missing %q", want)
		}
	}
	// both anti-affines against its OWN role (spread across nodes), not prefill/decode.
	if !strings.Contains(out, "llm-d.ai/role: prefill-decode\n                topologyKey: kubernetes.io/hostname") {
		t.Error("both should self-anti-affine (role: prefill-decode in the podAntiAffinity selector)")
	}
	// PRD-63 fix: a both pool present → the prefill profile uses the
	// prefill-only-filter (dedicated prefill pods only), NOT the stock
	// prefill-filter which would admit the both pod and self-route.
	if !strings.Contains(out, "prefill-only-filter") {
		t.Error("both pool present should emit the prefill-only-filter (anti-self-route)")
	}
}

// TestRenderLLMDDisaggregated_RoutingParams (PRD-61): custom EPP routing knobs
// render into the pd-config.yaml EndpointPickerConfig.
func TestRenderLLMDDisaggregated_RoutingParams(t *testing.T) {
	p := sampleDisaggParams()
	p.NonCachedTokens = 128
	p.PrefixCacheScorerWeight = 5
	p.QueueScorerWeight = 3
	p.MaxPrefixBlocksToMatch = 512
	p.LRUCapacityPerServer = 99999
	out, err := RenderLLMDDisaggregated(p)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"nonCachedTokens: 128",
		"maxPrefixBlocksToMatch: 512",
		"lruCapacityPerServer: 99999",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("routing config missing %q", want)
		}
	}
	// Shared weights appear in BOTH profiles → weight: 5 twice, weight: 3 twice.
	if n := strings.Count(out, "weight: 5"); n != 2 {
		t.Errorf("prefix-cache weight 5 should appear in both profiles (2), got %d", n)
	}
	if n := strings.Count(out, "weight: 3"); n != 2 {
		t.Errorf("queue weight 3 should appear in both profiles (2), got %d", n)
	}
	// The old defaults must be gone.
	if strings.Contains(out, "weight: 2") || strings.Contains(out, "nonCachedTokens: 16") {
		t.Error("custom routing params should replace the defaults")
	}
}

// TestRenderLLMDDisaggregated_RoutingDefaultsByteIdentical (PRD-61, load-bearing):
// at the shipped defaults, the pd-config.yaml is byte-identical to pre-PRD-61 —
// asserted for BOTH baselines (PD-only, and with a both pool, since PRD-63 forks
// the prefill filter ref). The default values are hardcoded here as the frozen
// contract; if they ever change, this test must change deliberately.
func TestRenderLLMDDisaggregated_RoutingDefaultsByteIdentical(t *testing.T) {
	// The exact EndpointPickerConfig lines the template shipped pre-PRD-61.
	wantLines := []string{
		"maxPrefixBlocksToMatch: 256",
		"lruCapacityPerServer: 31250",
		"nonCachedTokens: 16",
	}
	// PD-only baseline (BothReplicas 0 → stock prefill-filter).
	pdOnly, err := RenderLLMDDisaggregated(sampleDisaggParams())
	if err != nil {
		t.Fatal(err)
	}
	// With-both baseline (BothReplicas > 0 → prefill-only-filter).
	withBoth := sampleDisaggParams()
	withBoth.BothReplicas = 1
	withBoth.BothTP = 1
	wb, err := RenderLLMDDisaggregated(withBoth)
	if err != nil {
		t.Fatal(err)
	}
	for _, base := range []struct {
		name string
		out  string
	}{{"pd-only", pdOnly}, {"with-both", wb}} {
		for _, want := range wantLines {
			if !strings.Contains(base.out, want) {
				t.Errorf("[%s] default routing config missing %q", base.name, want)
			}
		}
		// Both profiles carry the default 2/1 weights → weight: 2 twice, weight: 1 twice.
		if n := strings.Count(base.out, "weight: 2"); n != 2 {
			t.Errorf("[%s] default prefix-cache weight 2 should appear twice, got %d", base.name, n)
		}
		if n := strings.Count(base.out, "weight: 1"); n != 2 {
			t.Errorf("[%s] default queue weight 1 should appear twice, got %d", base.name, n)
		}
		// Exact-sequence guards (a template comment/trim must not join YAML lines —
		// substring checks alone miss a "schedulingProfiles:- name" collapse).
		for _, seq := range []string{
			"    schedulingProfiles:\n    - name: prefill\n",
			"      parameters:\n        maxPrefixBlocksToMatch: 256\n        lruCapacityPerServer: 31250\n",
		} {
			if !strings.Contains(base.out, seq) {
				t.Errorf("[%s] EPP config formatting drifted; missing exact block:\n%q", base.name, seq)
			}
		}
	}
}

// TestBothRoleLabelMatchesConst (PRD-63): the wire value the template emits for
// the both pool must equal manifest.PDBothRoleLabel — the orchestrator's PD
// scraper matches observed pod labels against that constant, so drift would
// silently drop the both pod's metrics.
func TestBothRoleLabelMatchesConst(t *testing.T) {
	p := sampleDisaggParams()
	p.BothReplicas = 1
	p.BothTP = 1
	out, err := RenderLLMDDisaggregated(p)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "llm-d.ai/role: "+PDBothRoleLabel+"\n") {
		t.Errorf("template both role value must match PDBothRoleLabel (%q)", PDBothRoleLabel)
	}
}

// TestRenderLLMDDisaggregated_NoPrefillOnlyFilterWithoutBoth (PRD-63): a plain
// PD run (no both pool) keeps the stock prefill-filter — the anti-self-route
// filter swap only applies when a both pool is present.
func TestRenderLLMDDisaggregated_NoPrefillOnlyFilterWithoutBoth(t *testing.T) {
	out, err := RenderLLMDDisaggregated(sampleDisaggParams()) // BothReplicas 0
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "prefill-only-filter") {
		t.Error("PD-only run must not emit the prefill-only-filter")
	}
	if !strings.Contains(out, "pluginRef: prefill-filter") {
		t.Error("PD-only run should keep the stock prefill-filter ref")
	}
}

// TestRenderLLMDDisaggregated_BothPerRoleScheduler (PRD-63): BothServeArgs, when
// set, drives the both Deployment's --max-num-batched-tokens independently.
func TestRenderLLMDDisaggregated_BothPerRoleScheduler(t *testing.T) {
	p := sampleDisaggParams()
	p.BothReplicas = 1
	p.BothTP = 1
	base := []string{"Qwen/Qwen2.5-1.5B-Instruct", "--max-model-len", "4096"}
	p.ServeArgs = append(append([]string{}, base...), "--max-num-batched-tokens", "2048")
	p.BothServeArgs = append(append([]string{}, base...), "--max-num-batched-tokens", "9001")
	out, err := RenderLLMDDisaggregated(p)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"9001"`) {
		t.Error("both per-role batched-token override (9001) missing")
	}
}

// TestRenderLLMDDisaggregated_NoBothByDefault (PRD-63, load-bearing): with
// BothReplicas == 0 (the default), NO both objects render — the graph is the
// unchanged two-role prefill/decode shape.
func TestRenderLLMDDisaggregated_NoBothByDefault(t *testing.T) {
	out, err := RenderLLMDDisaggregated(sampleDisaggParams()) // BothReplicas 0
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "bench-abc12345-both") || strings.Contains(out, "llm-d.ai/role: both") {
		t.Error("BothReplicas==0 must render no both objects")
	}
	// Still exactly 3 Deployments (prefill, decode, EPP).
	if n := strings.Count(out, "kind: Deployment"); n != 3 {
		t.Errorf("expected 3 Deployments with no both pool, got %d", n)
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
