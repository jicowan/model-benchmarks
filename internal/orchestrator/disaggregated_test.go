package orchestrator

import (
	"context"
	"testing"

	"github.com/accelbench/accelbench/internal/database"
	"github.com/accelbench/accelbench/internal/manifest"

	k8sfake "k8s.io/client-go/kubernetes/fake"
)

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
		NonCachedTokens: 16, GPUDeviceClass: "gpu.nvidia.com",
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
