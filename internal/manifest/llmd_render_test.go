package manifest

import (
	"strings"
	"testing"
)

func sampleLLMDParams() LLMDDeploymentParams {
	return LLMDDeploymentParams{
		Name:                   "bench-abc12345",
		Namespace:              "accelbench",
		Image:                  "ghcr.io/llm-d/llm-d-aws:v0.8.1",
		ServeArgs:              []string{"meta-llama/Llama-3.1-70B", "--trust-remote-code"},
		ContainerName:          "vllm",
		ModelHfID:              "meta-llama/Llama-3.1-70B",
		HfToken:                "hf_test",
		ModelServiceAccount:    "accelbench-model",
		NodeCount:              2,
		TensorParallelDegree:   8,
		PipelineParallelDegree: 2,
		GPUsPerNode:            8,
		CPURequest:             "144",
		MemoryRequest:          "1800Gi",
		GPUDeviceClass:         "gpu.nvidia.com",
		EFADeviceClass:         "efa.networking.k8s.aws",
		EFAPerNode:             8,
		GatewayName:            "accelbench-gateway",
		GatewayNamespace:       "envoy-gateway-system",
		MultiNodeTaintKey:      "accelbench.io/multinode",
		MultiNodeTaintValue:    "true",
		DRANodeSelectorKey:     "accelbench.io/dra",
		DRANodeSelectorVal:     "true",
		InstanceTypeName:       "p5.48xlarge",
	}
}

// TestRenderLLMDDeployment_InstanceTypePinned: the co-located (PP) group pins the
// run's selected instance type on every pod.
func TestRenderLLMDDeployment_InstanceTypePinned(t *testing.T) {
	out, err := RenderLLMDDeployment(sampleLLMDParams())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "node.kubernetes.io/instance-type: p5.48xlarge") {
		t.Error("LWS pods must pin the run's instance type")
	}
}

func TestRenderLLMDDeployment_ObjectGraph(t *testing.T) {
	out, err := RenderLLMDDeployment(sampleLLMDParams())
	if err != nil {
		t.Fatalf("RenderLLMDDeployment: %v", err)
	}

	// All four document kinds present (InferencePool + EPP deferred to PRD-58).
	for _, kind := range []string{
		"kind: ResourceClaimTemplate",
		"kind: LeaderWorkerSet",
		"kind: Service",
		"kind: HTTPRoute",
	} {
		if !strings.Contains(out, kind) {
			t.Errorf("manifest set missing %q", kind)
		}
	}
	// InferencePool must NOT be rendered in PRD-56.
	if strings.Contains(out, "kind: InferencePool") {
		t.Error("InferencePool should be deferred to PRD-58, not rendered")
	}
	// HTTPRoute backends the "-svc" Service (NOT "<name>", which is the LWS
	// controller's own headless Service).
	if !strings.Contains(out, "name: bench-abc12345-svc") {
		t.Error("HTTPRoute/Service should use the -svc name to avoid the LWS headless-Service collision")
	}
	// LLM inference exceeds Envoy Gateway's default 15s request timeout — the
	// route must raise it or slow requests 504 (root cause of the 600/600
	// loadgen failures).
	if !strings.Contains(out, "timeouts:") || !strings.Contains(out, `request: "3600s"`) {
		t.Error("HTTPRoute must set a generous request timeout (default 15s 504s LLM requests)")
	}
	if !strings.Contains(out, "port: 8000") {
		t.Error("HTTPRoute should backendRef the Service on port 8000")
	}

	// LWS group size == node count.
	if !strings.Contains(out, "size: 2") {
		t.Error("LWS size should be 2")
	}
	// DRA claims (GPU + EFA), PCIe alignment.
	for _, want := range []string{
		"deviceClassName: gpu.nvidia.com",
		"count: 8",
		"deviceClassName: efa.networking.k8s.aws",
		`matchAttribute: "resource.kubernetes.io/pcieRoot"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing DRA wiring %q", want)
		}
	}
	// EFA / NCCL env.
	for _, want := range []string{"FI_PROVIDER", "NCCL_DEBUG"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing EFA env %q", want)
		}
	}
	// Cross-node socket pinning: GLOO_SOCKET_IFNAME (PP CPU-side coordination)
	// + VLLM_HOST_IP from the pod IP — required so multi-node PP doesn't hang.
	for _, want := range []string{"GLOO_SOCKET_IFNAME", "VLLM_HOST_IP", "fieldPath: status.podIP"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing multi-node network env %q", want)
		}
	}
	// Gateway parentRef binds to the shared gateway.
	if !strings.Contains(out, "name: accelbench-gateway") {
		t.Error("HTTPRoute should parentRef the shared gateway")
	}
	// Scheduling onto the PRD-55 static pool.
	if !strings.Contains(out, "accelbench.io/multinode") {
		t.Error("pods should tolerate the multinode taint")
	}
	if !strings.Contains(out, `accelbench.io/dra: "true"`) {
		t.Error("pods should nodeSelect the DRA label")
	}
}

func TestRenderLLMDDeployment_PipelineParallelLaunch(t *testing.T) {
	out, err := RenderLLMDDeployment(sampleLLMDParams())
	if err != nil {
		t.Fatal(err)
	}
	// vLLM multi-node MultiProcessing backend (no Ray) — pipeline-parallel
	// SPLITS layers across nodes. --node-rank/--master-addr/--nnodes from LWS.
	if strings.Contains(out, "ray start") {
		t.Error("must NOT use Ray — vLLM MultiProcessing backend")
	}
	if strings.Contains(out, "--data-parallel") {
		t.Error("must NOT use data-parallel — DP replicates the model, doesn't split layers")
	}
	for _, want := range []string{
		"exec vllm serve",
		"meta-llama/Llama-3.1-70B",
		"--pipeline-parallel-size ${PP_SIZE}",
		"--tensor-parallel-size ${TP_SIZE}",
		"--nnodes ${NNODES}",
		"--node-rank ${NODE_RANK}",
		"--master-addr ${LWS_LEADER_ADDRESS}",
		"NODE_RANK=${LWS_WORKER_INDEX:-0}",
		"NNODES=${LWS_GROUP_SIZE:-2}",
		// Head (node-rank 0) serves; secondaries go headless.
		`HEADLESS="--headless"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("PP launch missing %q", want)
		}
	}
	// Same launch on BOTH leader and worker templates → serve appears twice.
	if n := strings.Count(out, "exec vllm serve"); n != 2 {
		t.Errorf("every pod runs vllm serve (leader+worker) → expect 2, got %d", n)
	}
}

func TestRenderLLMDDeployment_LeaderOnlyReadiness(t *testing.T) {
	out, err := RenderLLMDDeployment(sampleLLMDParams())
	if err != nil {
		t.Fatal(err)
	}
	// Exactly one readinessProbe — the leader's. Workers omit it (they never
	// answer /health themselves).
	if got := strings.Count(out, "readinessProbe:"); got != 1 {
		t.Errorf("expected exactly 1 readinessProbe (leader only), got %d", got)
	}
}

func TestRenderLLMDDeployment_NoEFA(t *testing.T) {
	p := sampleLLMDParams()
	p.EFAPerNode = 0
	out, err := RenderLLMDDeployment(p)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "efa.networking.k8s.aws") {
		t.Error("EFA claim should be omitted when EFAPerNode == 0")
	}
	// GPU claim still present.
	if !strings.Contains(out, "gpu.nvidia.com") {
		t.Error("GPU claim must remain when EFA is off")
	}
}

func TestRenderLLMDDeployment_EFAMode_Default(t *testing.T) {
	// sampleLLMDParams leaves NetworkMode empty; deployLLMD normalizes to
	// "efa", but the template treats anything != "tcp" as EFA. Verify EFA env.
	p := sampleLLMDParams()
	p.NetworkMode = "efa"
	out, err := RenderLLMDDeployment(p)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"FI_PROVIDER", `value: "efa"`, "FI_EFA_USE_DEVICE_RDMA"} {
		if !strings.Contains(out, want) {
			t.Errorf("EFA mode should emit %q", want)
		}
	}
	if strings.Contains(out, "NCCL_NET") {
		t.Error("EFA mode should not set NCCL_NET=Socket")
	}
}

func TestRenderLLMDDeployment_TCPMode(t *testing.T) {
	p := sampleLLMDParams()
	p.NetworkMode = "tcp"
	p.EFAPerNode = 0 // deployLLMD zeroes this in TCP mode
	out, err := RenderLLMDDeployment(p)
	if err != nil {
		t.Fatal(err)
	}
	// Socket fabric env present; EFA env absent.
	for _, want := range []string{`name: NCCL_NET`, `value: "Socket"`, "NCCL_SOCKET_IFNAME"} {
		if !strings.Contains(out, want) {
			t.Errorf("TCP mode should emit %q", want)
		}
	}
	if strings.Contains(out, "FI_PROVIDER") {
		t.Error("TCP mode must not set FI_PROVIDER=efa")
	}
	// No EFA device claim.
	if strings.Contains(out, "efa.networking.k8s.aws") {
		t.Error("TCP mode must not render an EFA ResourceClaimTemplate")
	}
}
