package orchestrator

import (
	"context"
	"strings"
	"testing"

	"github.com/accelbench/accelbench/internal/database"
	"github.com/accelbench/accelbench/internal/manifest"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sfake "k8s.io/client-go/kubernetes/fake"
)

// TestDeployLLMDDisaggregated_StreamerParity (PRD-65 Layers 2+3): a D/P deploy
// of a CACHED model auto-detects the S3 URI + Run:ai, and threads the
// memory-limit through to the rendered pod (env + extra-config). End-to-end:
// cfg → resolveS3Model → buildServeArgs → render → applied Deployment.
func TestDeployLLMDDisaggregated_StreamerParity(t *testing.T) {
	strptr := func(s string) *string { return &s }
	repo := database.NewMockRepo()
	_, _ = repo.CreateModelCache(context.Background(), &database.ModelCache{
		HfID: strptr("Qwen/Qwen2.5-1.5B-Instruct"), HfRevision: "main",
		S3URI: "s3://accelbench-models/qwen", Status: "cached",
	})
	dyn := newFakeDyn()
	o := &Orchestrator{client: k8sfake.NewSimpleClientset(), repo: repo, dynClient: dyn}

	cfg := RunConfig{
		RunID: "run-pd-0001",
		Request: &database.RunRequest{
			Framework:              "llm-d",
			ModelHfID:              "Qwen/Qwen2.5-1.5B-Instruct",
			DeploymentMode:         "disaggregated",
			StreamerMemoryLimitGiB: 16, // explicit → 16 GiB = 17179869184 bytes
		},
		InstanceType:    &database.InstanceType{Name: "g6.2xlarge", MemoryGiB: 32, VCPUs: 8, AcceleratorName: "L4"},
		PrefillReplicas: 1, PrefillTP: 1, DecodeReplicas: 1, DecodeTP: 1,
	}

	if err := o.deployLLMDDisaggregated(context.Background(), "accelbench", "bench-run-pd-0001", cfg); err != nil {
		t.Fatalf("deployLLMDDisaggregated: %v", err)
	}

	// Read back a role Deployment and assert the streamer wiring landed.
	list, err := dyn.Resource(crdGVRTable["apps/v1|Deployment"]).Namespace("accelbench").List(context.Background(), metav1.ListOptions{})
	if err != nil || len(list.Items) == 0 {
		t.Fatalf("no Deployments applied: err=%v", err)
	}
	blob, _ := list.Items[0].MarshalJSON()
	s := string(blob)
	for _, want := range []string{
		"runai_streamer",              // auto-detected cached model → streamer on
		"s3://accelbench-models/qwen", // the cached S3 URI as the model arg
		"memory_limit",                // Layer 3: memory-limit in extra-config
		"RUNAI_STREAMER_MEMORY_LIMIT", // Layer 3: env on the container
		"17179869184",                 // 16 GiB in bytes (extra-config + env value)
	} {
		if !strings.Contains(s, want) {
			t.Errorf("applied D/P Deployment missing %q", want)
		}
	}
}

// TestPDModelImage covers the D/P vLLM image resolver (PRD-66 Part 2): composes
// vllm/vllm-openai from the version, defaults an empty version, and prefixes the
// Docker Hub pull-through cache when a registry is given.
func TestPDModelImage(t *testing.T) {
	cases := []struct {
		version, pt, want string
	}{
		{"v0.25.0", "", "vllm/vllm-openai:v0.25.0"},
		{"v0.26.1", "", "vllm/vllm-openai:v0.26.1"},
		{"", "", "vllm/vllm-openai:" + DefaultPDVLLMVersion},
		{"v0.25.0", "123.dkr.ecr.us-east-2.amazonaws.com", "123.dkr.ecr.us-east-2.amazonaws.com/dockerhub/vllm/vllm-openai:v0.25.0"},
		{"", "123.dkr.ecr.us-east-2.amazonaws.com", "123.dkr.ecr.us-east-2.amazonaws.com/dockerhub/vllm/vllm-openai:" + DefaultPDVLLMVersion},
	}
	for _, c := range cases {
		if got := PDModelImage(c.version, c.pt); got != c.want {
			t.Errorf("PDModelImage(%q,%q) = %q, want %q", c.version, c.pt, got, c.want)
		}
	}
}

// TestApplyDisaggregatedManifestSet renders the full PD-disaggregated object
// graph and applies it through the dynamic client, asserting every kind maps to
// a GVR (no "no GVR mapping" errors) and that the cluster-scoped RBAC lands
// without a namespace. This is the end-to-end guard for the dynamic.go GVR
// table + cluster-scoped handling added in PRD-58.
func TestApplyDisaggregatedManifestSet(t *testing.T) {
	yamlStr, err := manifest.RenderLLMDDisaggregated(manifest.LLMDDisaggregatedParams{
		Name: "bench-xyz98765", Namespace: "accelbench",
		Image:     "vllm/vllm-openai:v0.25.0",
		ServeArgs: []string{"Qwen/Qwen2.5-1.5B-Instruct", "--trust-remote-code"},
		ContainerName: "vllm", ModelHfID: "Qwen/Qwen2.5-1.5B-Instruct",
		ModelLabel: "qwen2-5-1-5b-instruct",
		PrefillReplicas: 2, PrefillTP: 1, DecodeReplicas: 1, DecodeTP: 2,
		CPURequest: "3", MemoryRequest: "12Gi", NetworkMode: "tcp",
		NixlModuleDir: "/x/ucx", EPPImage: "epp:v0.9.0", SidecarImage: "sidecar:v0.9.0",
		NonCachedTokens: 16, PrefixCacheScorerWeight: 2, QueueScorerWeight: 1, MaxPrefixBlocksToMatch: 256, LRUCapacityPerServer: 31250, GPUDeviceClass: "gpu.nvidia.com",
		GatewayName: "accelbench-gateway", GatewayNamespace: "envoy-gateway-system",
		MultiNodeTaintKey: "accelbench.io/multinode", MultiNodeTaintValue: "true",
		DRANodeSelectorKey: "accelbench.io/dra", DRANodeSelectorVal: "true",
	})
	if err != nil {
		t.Fatalf("render: %v", err)
	}

	o := &Orchestrator{
		client:    k8sfake.NewSimpleClientset(),
		repo:      database.NewMockRepo(),
		dynClient: newFakeDyn(),
	}

	applied, err := o.applyManifestSet(context.Background(), "accelbench", yamlStr)
	if err != nil {
		t.Fatalf("applyManifestSet: %v", err)
	}
	// 16 documents in the graph (2 RCT, 2 role Deployments, 2 role Services,
	// InferencePool, ConfigMap, SA, Role, RoleBinding, ClusterRole,
	// ClusterRoleBinding, EPP Deployment, EPP Service, HTTPRoute).
	if len(applied) != 16 {
		t.Errorf("expected 16 applied objects, got %d", len(applied))
	}

	// The two cluster-scoped RBAC objects must be flagged and applied.
	var clusterScoped int
	for _, a := range applied {
		if a.clusterScoped {
			clusterScoped++
		}
	}
	if clusterScoped != 2 {
		t.Errorf("expected 2 cluster-scoped objects (ClusterRole + ClusterRoleBinding), got %d", clusterScoped)
	}

	// Teardown deletes the whole graph without error (idempotent, best-effort).
	for i := len(applied) - 1; i >= 0; i-- {
		if err := o.deleteUnstructured(context.Background(), "accelbench", applied[i]); err != nil {
			t.Errorf("delete %s/%s: %v", applied[i].gvr.Resource, applied[i].name, err)
		}
	}
}

// TestApplyDisaggregatedWithBothRole (PRD-63): a graph that adds a co-located
// "both" pool applies cleanly through the dynamic client — the extra RCT,
// Deployment, and Service (3 objects) land on top of the 16-object PD graph.
func TestApplyDisaggregatedWithBothRole(t *testing.T) {
	yamlStr, err := manifest.RenderLLMDDisaggregated(manifest.LLMDDisaggregatedParams{
		Name: "bench-both1234", Namespace: "accelbench",
		Image:     "vllm/vllm-openai:v0.25.0",
		ServeArgs: []string{"Qwen/Qwen2.5-1.5B-Instruct"},
		ContainerName: "vllm", ModelHfID: "Qwen/Qwen2.5-1.5B-Instruct",
		ModelLabel: "qwen2-5-1-5b-instruct",
		PrefillReplicas: 1, PrefillTP: 1, DecodeReplicas: 1, DecodeTP: 1,
		BothReplicas: 2, BothTP: 1,
		CPURequest: "3", MemoryRequest: "12Gi", NetworkMode: "tcp",
		NixlModuleDir: "/x/ucx", EPPImage: "epp:v0.9.0", SidecarImage: "sidecar:v0.9.0",
		NonCachedTokens: 16, PrefixCacheScorerWeight: 2, QueueScorerWeight: 1, MaxPrefixBlocksToMatch: 256, LRUCapacityPerServer: 31250, GPUDeviceClass: "gpu.nvidia.com",
		GatewayName: "accelbench-gateway", GatewayNamespace: "envoy-gateway-system",
		MultiNodeTaintKey: "accelbench.io/multinode", MultiNodeTaintValue: "true",
		DRANodeSelectorKey: "accelbench.io/dra", DRANodeSelectorVal: "true",
	})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	o := &Orchestrator{
		client:    k8sfake.NewSimpleClientset(),
		repo:      database.NewMockRepo(),
		dynClient: newFakeDyn(),
	}
	applied, err := o.applyManifestSet(context.Background(), "accelbench", yamlStr)
	if err != nil {
		t.Fatalf("applyManifestSet: %v", err)
	}
	// 16 (PD graph) + 3 (both RCT + Deployment + Service) = 19.
	if len(applied) != 19 {
		t.Errorf("expected 19 applied objects with a both pool, got %d", len(applied))
	}
	var foundBothDep bool
	for _, a := range applied {
		if a.name == "bench-both1234-both" && a.gvr.Resource == "deployments" {
			foundBothDep = true
		}
	}
	if !foundBothDep {
		t.Error("both Deployment not applied")
	}
	for i := len(applied) - 1; i >= 0; i-- {
		_ = o.deleteUnstructured(context.Background(), "accelbench", applied[i])
	}
}

// TestApplyDisaggregatedBothOnly (PRD-63): a both-only run (prefill=0, decode=0)
// applies only the both role + shared routing graph — 14 objects (16 minus the
// 2 prefill/decode RCTs and 2 role Services minus... precisely: both RCT +
// both Deployment + both Service + InferencePool + ConfigMap + SA + Role +
// RoleBinding + ClusterRole + ClusterRoleBinding + EPP Deployment + EPP Service +
// HTTPRoute = 13).
func TestApplyDisaggregatedBothOnly(t *testing.T) {
	yamlStr, err := manifest.RenderLLMDDisaggregated(manifest.LLMDDisaggregatedParams{
		Name: "bench-bo123456", Namespace: "accelbench",
		Image:     "vllm/vllm-openai:v0.25.0",
		ServeArgs: []string{"Qwen/Qwen2.5-1.5B-Instruct"},
		ContainerName: "vllm", ModelHfID: "Qwen/Qwen2.5-1.5B-Instruct",
		ModelLabel: "qwen2-5-1-5b-instruct",
		PrefillReplicas: 0, DecodeReplicas: 0, BothReplicas: 2, BothTP: 1,
		CPURequest: "3", MemoryRequest: "12Gi", NetworkMode: "tcp",
		NixlModuleDir: "/x/ucx", EPPImage: "epp:v0.9.0", SidecarImage: "sidecar:v0.9.0",
		NonCachedTokens: 16, PrefixCacheScorerWeight: 2, QueueScorerWeight: 1, MaxPrefixBlocksToMatch: 256, LRUCapacityPerServer: 31250, GPUDeviceClass: "gpu.nvidia.com",
		GatewayName: "accelbench-gateway", GatewayNamespace: "envoy-gateway-system",
		MultiNodeTaintKey: "accelbench.io/multinode", MultiNodeTaintValue: "true",
		DRANodeSelectorKey: "accelbench.io/dra", DRANodeSelectorVal: "true",
	})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	o := &Orchestrator{
		client:    k8sfake.NewSimpleClientset(),
		repo:      database.NewMockRepo(),
		dynClient: newFakeDyn(),
	}
	applied, err := o.applyManifestSet(context.Background(), "accelbench", yamlStr)
	if err != nil {
		t.Fatalf("applyManifestSet: %v", err)
	}
	if len(applied) != 13 {
		t.Errorf("expected 13 applied objects for a both-only run, got %d", len(applied))
	}
	for _, a := range applied {
		if a.name == "bench-bo123456-prefill" || a.name == "bench-bo123456-decode" {
			t.Errorf("both-only run should not apply %s", a.name)
		}
	}
	for i := len(applied) - 1; i >= 0; i-- {
		_ = o.deleteUnstructured(context.Background(), "accelbench", applied[i])
	}
}
