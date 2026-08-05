package orchestrator

import (
	"context"
	"testing"

	"github.com/accelbench/accelbench/internal/database"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynfake "k8s.io/client-go/dynamic/fake"
	k8sfake "k8s.io/client-go/kubernetes/fake"
)

// dynScheme + list-kind mapping so the fake dynamic client can serve List on
// our CRDs (LWS + NodePool + EC2NodeClass) and the core Service.
func newFakeDyn(objs ...runtime.Object) *dynfake.FakeDynamicClient {
	scheme := runtime.NewScheme()
	gvrToList := map[schema.GroupVersionResource]string{
		gvrNodePool:     "NodePoolList",
		gvrEC2NodeClass: "EC2NodeClassList",
		crdGVRTable["leaderworkerset.x-k8s.io/v1|LeaderWorkerSet"]: "LeaderWorkerSetList",
		crdGVRTable["gateway.networking.k8s.io/v1|HTTPRoute"]:      "HTTPRouteList",
		crdGVRTable["resource.k8s.io/v1|ResourceClaimTemplate"]:    "ResourceClaimTemplateList",
		crdGVRTable["v1|Service"]:                                  "ServiceList",
		// PRD-58 disaggregated graph kinds.
		crdGVRTable["inference.networking.k8s.io/v1|InferencePool"]:      "InferencePoolList",
		crdGVRTable["apps/v1|Deployment"]:                               "DeploymentList",
		crdGVRTable["v1|ConfigMap"]:                                     "ConfigMapList",
		crdGVRTable["v1|ServiceAccount"]:                                "ServiceAccountList",
		crdGVRTable["rbac.authorization.k8s.io/v1|Role"]:                "RoleList",
		crdGVRTable["rbac.authorization.k8s.io/v1|RoleBinding"]:         "RoleBindingList",
		crdGVRTable["rbac.authorization.k8s.io/v1|ClusterRole"]:         "ClusterRoleList",
		crdGVRTable["rbac.authorization.k8s.io/v1|ClusterRoleBinding"]:  "ClusterRoleBindingList",
	}
	return dynfake.NewSimpleDynamicClientWithCustomListKinds(scheme, gvrToList, objs...)
}

// nodePoolObj builds an AZ-pinned NodePool (has a topology.kubernetes.io/zone
// requirement) — the shape auto-select considers. Use nodePoolObjNoAZ for a
// no-AZ scratch pool.
func nodePoolObj(name string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "karpenter.sh/v1",
		"kind":       "NodePool",
		"metadata":   map[string]any{"name": name},
		"spec": map[string]any{
			"replicas": int64(0),
			"template": map[string]any{
				"spec": map[string]any{
					"requirements": []any{
						map[string]any{"key": "topology.kubernetes.io/zone", "operator": "In", "values": []any{"us-east-2a"}},
					},
				},
			},
		},
	}}
}

// nodePoolObjNoAZ builds a NodePool with NO AZ constraint (a scratch pool).
func nodePoolObjNoAZ(name string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "karpenter.sh/v1",
		"kind":       "NodePool",
		"metadata":   map[string]any{"name": name},
		"spec":       map[string]any{"replicas": int64(0)},
	}}
}

func ec2NodeClassObj(name string, reserved bool) *unstructured.Unstructured {
	spec := map[string]any{}
	if reserved {
		spec["capacityReservationSelectorTerms"] = []any{
			map[string]any{"id": "cr-123"},
		}
	}
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "karpenter.k8s.aws/v1",
		"kind":       "EC2NodeClass",
		"metadata":   map[string]any{"name": name},
		"spec":       spec,
	}}
}

func distributedRunConfig(runID string, nodeCount int) RunConfig {
	return RunConfig{
		RunID: runID,
		Model: &database.Model{ID: "m1", HfID: "meta-llama/Llama-3.1-70B"},
		InstanceType: &database.InstanceType{
			Name: "p5.48xlarge", Family: "p5", AcceleratorType: "gpu",
			AcceleratorName: "H100", AcceleratorCount: 8, AcceleratorMemoryGiB: 640,
			VCPUs: 192, MemoryGiB: 2048,
		},
		Request: &database.RunRequest{
			ModelHfID: "meta-llama/Llama-3.1-70B", InstanceTypeName: "p5.48xlarge",
			Framework: "llm-d", FrameworkVersion: "0.2.0", TensorParallelDegree: 8,
			ScenarioID: "chatbot",
		},
		NodeCount:              nodeCount,
		PipelineParallelDegree: 2,
	}
}

func TestIsDistributed(t *testing.T) {
	cfg := distributedRunConfig("run-1", 2)
	if !cfg.IsDistributed() {
		t.Error("llm-d with NodeCount=2 should be distributed")
	}
	// Single-node llm-d is not distributed.
	cfg.NodeCount = 1
	if cfg.IsDistributed() {
		t.Error("NodeCount=1 should not be distributed")
	}
	// vLLM never distributed even with a node count.
	cfg.NodeCount = 4
	cfg.Request.Framework = "vllm"
	if cfg.IsDistributed() {
		t.Error("vllm should never be distributed")
	}
}

func TestIsDisaggregated(t *testing.T) {
	// PRD-58: a disaggregated run is a subset of distributed — needs
	// DeploymentMode "disaggregated" AND multi-node (NodeCount>1).
	cfg := distributedRunConfig("run-1", 3)
	cfg.Request.DeploymentMode = "disaggregated"
	cfg.PrefillReplicas, cfg.DecodeReplicas = 2, 1
	if !cfg.IsDisaggregated() {
		t.Error("llm-d disaggregated with NodeCount=3 should be disaggregated")
	}
	if !cfg.IsDistributed() {
		t.Error("disaggregated implies distributed")
	}
	// Co-located distributed is NOT disaggregated.
	cfg.Request.DeploymentMode = "distributed"
	if cfg.IsDisaggregated() {
		t.Error("distributed (co-located) is not disaggregated")
	}
	// Single-node disaggregated is not distributed → not disaggregated.
	cfg.Request.DeploymentMode = "disaggregated"
	cfg.NodeCount = 1
	if cfg.IsDisaggregated() {
		t.Error("NodeCount=1 should not be disaggregated")
	}
}

func TestAZFromPoolName(t *testing.T) {
	cases := map[string]string{
		"multinode-us-east-2a": "us-east-2a",
		"multinode-us-east-2c": "us-east-2c",
		"multinode-tcp":        "", // name-only, no AZ → EPP unconstrained
		"multinode-test":       "", // scratch pool → no AZ
		"gpu":                  "", // not a multinode pool
		"":                     "",
	}
	for in, want := range cases {
		if got := azFromPoolName(in); got != want {
			t.Errorf("azFromPoolName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestModelLabelValue(t *testing.T) {
	cases := map[string]string{
		"Qwen/Qwen2.5-1.5B-Instruct":  "qwen-qwen2-5-1-5b-instruct",
		"meta-llama/Llama-3.1-70B":    "meta-llama-llama-3-1-70b",
		"":                            "model",
		"___":                         "model",
	}
	for in, want := range cases {
		if got := modelLabelValue(in); got != want {
			t.Errorf("modelLabelValue(%q) = %q, want %q", in, got, want)
		}
	}
	// DNS-1123 label bounds: <=63 chars, lowercase alnum + '-', no leading/trailing '-'.
	long := modelLabelValue("Org/" + string(make([]byte, 200)))
	if len(long) > 63 {
		t.Errorf("label too long: %d", len(long))
	}
}

func TestResolveCRDGVR(t *testing.T) {
	gvr, err := resolveCRDGVR("leaderworkerset.x-k8s.io/v1", "LeaderWorkerSet")
	if err != nil {
		t.Fatal(err)
	}
	if gvr.Resource != "leaderworkersets" {
		t.Errorf("resource = %q", gvr.Resource)
	}
	if _, err := resolveCRDGVR("bogus/v1", "Nope"); err == nil {
		t.Error("expected error for unknown kind")
	}
}

func TestSelectMultinodePool_PrefersReserved(t *testing.T) {
	dyn := newFakeDyn(
		nodePoolObj("multinode-us-east-2a"),
		nodePoolObj("multinode-us-east-2b"),
		nodePoolObj("gpu"), // non-multinode pool must be ignored
		ec2NodeClassObj("multinode-us-east-2a", false),
		ec2NodeClassObj("multinode-us-east-2b", true), // reserved
	)
	o := New(k8sfake.NewSimpleClientset(), database.NewMockRepo(), "pod")
	o.SetDynamicClient(dyn)

	pools, err := o.selectMultinodePool(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(pools) != 2 {
		t.Fatalf("expected 2 multinode pools, got %v", pools)
	}
	if pools[0] != "multinode-us-east-2b" {
		t.Errorf("reserved pool should sort first; got %v", pools)
	}
}

// TestSelectMultinodePool_ExcludesNoAZPool: auto-select must NOT consider a
// multinode pool that lacks an AZ constraint (a scratch pool) — it could spread
// a run's nodes across AZs, breaking same-AZ NCCL/NIXL. Only AZ-pinned pools are
// candidates, regardless of name / alphabetical order.
func TestSelectMultinodePool_ExcludesNoAZPool(t *testing.T) {
	dyn := newFakeDyn(
		nodePoolObjNoAZ("multinode-scratch"), // no AZ requirement → must be skipped
		nodePoolObj("multinode-us-east-2a"),
		nodePoolObj("multinode-us-east-2b"),
	)
	o := New(k8sfake.NewSimpleClientset(), database.NewMockRepo(), "pod")
	o.SetDynamicClient(dyn)

	pools, err := o.selectMultinodePool(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range pools {
		if p == "multinode-scratch" {
			t.Errorf("auto-select must exclude the no-AZ pool; got %v", pools)
		}
	}
	if len(pools) != 2 {
		t.Fatalf("expected only the 2 AZ-pinned pools, got %v", pools)
	}
}

// TestSelectMultinodePool_NoAZPoolViaOverride: a no-AZ pool is still reachable
// when explicitly requested (the override path bypasses auto-select entirely).
func TestSelectMultinodePool_NoAZPoolViaOverride(t *testing.T) {
	dyn := newFakeDyn(nodePoolObjNoAZ("multinode-scratch"), nodePoolObj("multinode-us-east-2a"))
	o := New(k8sfake.NewSimpleClientset(), database.NewMockRepo(), "pod")
	o.SetDynamicClient(dyn)
	pools, err := o.selectMultinodePool(context.Background(), "multinode-scratch")
	if err != nil {
		t.Fatal(err)
	}
	if len(pools) != 1 || pools[0] != "multinode-scratch" {
		t.Errorf("explicit override should yield the requested pool; got %v", pools)
	}
}

// TestSetNodePoolInstanceType: the orchestrator narrows a static pool to the
// run's exact instance type before scale-out — replacing any instance-category
// (family) constraint, preserving the AZ requirement, so the pool provisions
// exactly what the run selected (and the pods' matching nodeSelector agrees).
func TestSetNodePoolInstanceType(t *testing.T) {
	// A pool that currently uses the family category (like the widened pools).
	pool := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "karpenter.sh/v1",
		"kind":       "NodePool",
		"metadata":   map[string]any{"name": "multinode-us-east-2a"},
		"spec": map[string]any{
			"replicas": int64(0),
			"template": map[string]any{
				"spec": map[string]any{
					"requirements": []any{
						map[string]any{"key": "kubernetes.io/arch", "operator": "In", "values": []any{"amd64"}},
						map[string]any{"key": "karpenter.k8s.aws/instance-category", "operator": "In", "values": []any{"g", "p"}},
						map[string]any{"key": "topology.kubernetes.io/zone", "operator": "In", "values": []any{"us-east-2a"}},
					},
				},
			},
		},
	}}
	dyn := newFakeDyn(pool)
	o := New(k8sfake.NewSimpleClientset(), database.NewMockRepo(), "pod")
	o.SetDynamicClient(dyn)

	if err := o.setNodePoolInstanceType(context.Background(), "multinode-us-east-2a", "g6.12xlarge"); err != nil {
		t.Fatalf("setNodePoolInstanceType: %v", err)
	}
	got, err := dyn.Resource(gvrNodePool).Get(context.Background(), "multinode-us-east-2a", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	reqs, _, _ := unstructured.NestedSlice(got.Object, "spec", "template", "spec", "requirements")
	var haveType, haveZone, haveArch bool
	for _, r := range reqs {
		m := r.(map[string]any)
		switch m["key"].(string) {
		case "node.kubernetes.io/instance-type":
			haveType = true
			vals, _, _ := unstructured.NestedStringSlice(m, "values")
			if len(vals) != 1 || vals[0] != "g6.12xlarge" {
				t.Errorf("instance-type should be exactly [g6.12xlarge], got %v", vals)
			}
		case "karpenter.k8s.aws/instance-category":
			t.Error("instance-category should be removed when a specific type is set")
		case "topology.kubernetes.io/zone":
			haveZone = true
		case "kubernetes.io/arch":
			haveArch = true
		}
	}
	if !haveType {
		t.Error("instance-type requirement not set")
	}
	if !haveZone || !haveArch {
		t.Error("zone/arch requirements must be preserved")
	}
}

// TestResetNodePoolInstanceType: after a run pins one instance-type, reset drops
// it and restores the broad instance-category In [g,p] constraint, preserving
// zone/arch. This is what keeps a pool from staying pinned to the last run's
// type (which would silently narrow the next run's provisioning).
func TestResetNodePoolInstanceType(t *testing.T) {
	// A pool left pinned to one type by a prior run (no category key).
	pool := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "karpenter.sh/v1",
		"kind":       "NodePool",
		"metadata":   map[string]any{"name": "multinode-us-east-2a"},
		"spec": map[string]any{
			"replicas": int64(0),
			"template": map[string]any{
				"spec": map[string]any{
					"requirements": []any{
						map[string]any{"key": "kubernetes.io/arch", "operator": "In", "values": []any{"amd64"}},
						map[string]any{"key": "node.kubernetes.io/instance-type", "operator": "In", "values": []any{"g6.12xlarge"}},
						map[string]any{"key": "topology.kubernetes.io/zone", "operator": "In", "values": []any{"us-east-2a"}},
					},
				},
			},
		},
	}}
	dyn := newFakeDyn(pool)
	o := New(k8sfake.NewSimpleClientset(), database.NewMockRepo(), "pod")
	o.SetDynamicClient(dyn)

	if err := o.resetNodePoolInstanceType(context.Background(), "multinode-us-east-2a"); err != nil {
		t.Fatalf("resetNodePoolInstanceType: %v", err)
	}
	got, err := dyn.Resource(gvrNodePool).Get(context.Background(), "multinode-us-east-2a", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	reqs, _, _ := unstructured.NestedSlice(got.Object, "spec", "template", "spec", "requirements")
	var haveCat, haveZone, haveArch bool
	for _, r := range reqs {
		m := r.(map[string]any)
		switch m["key"].(string) {
		case "node.kubernetes.io/instance-type":
			t.Error("instance-type pin should be removed on reset")
		case "karpenter.k8s.aws/instance-category":
			haveCat = true
			vals, _, _ := unstructured.NestedStringSlice(m, "values")
			if len(vals) != 2 || vals[0] != "g" || vals[1] != "p" {
				t.Errorf("instance-category should be [g p], got %v", vals)
			}
		case "topology.kubernetes.io/zone":
			haveZone = true
		case "kubernetes.io/arch":
			haveArch = true
		}
	}
	if !haveCat {
		t.Error("instance-category requirement not restored")
	}
	if !haveZone || !haveArch {
		t.Error("zone/arch requirements must be preserved")
	}
}

// TestSetNodePoolNetworkMode: TCP runs repoint the pool's nodeClassRef at the
// non-EFA multinode-tcp-<az> class (so non-EFA instances can launch); EFA runs
// use the pool's own EFA class.
func TestSetNodePoolNetworkMode(t *testing.T) {
	mkPool := func() *unstructured.Unstructured {
		return &unstructured.Unstructured{Object: map[string]any{
			"apiVersion": "karpenter.sh/v1", "kind": "NodePool",
			"metadata": map[string]any{"name": "multinode-us-east-2a"},
			"spec": map[string]any{"replicas": int64(0), "template": map[string]any{"spec": map[string]any{
				"nodeClassRef": map[string]any{"group": "karpenter.k8s.aws", "kind": "EC2NodeClass", "name": "multinode-us-east-2a"},
			}}},
		}}
	}
	// TCP → multinode-tcp-us-east-2a
	dyn := newFakeDyn(mkPool())
	o := New(k8sfake.NewSimpleClientset(), database.NewMockRepo(), "pod")
	o.SetDynamicClient(dyn)
	if err := o.setNodePoolNetworkMode(context.Background(), "multinode-us-east-2a", NetworkModeTCP); err != nil {
		t.Fatal(err)
	}
	got, _ := dyn.Resource(gvrNodePool).Get(context.Background(), "multinode-us-east-2a", metav1.GetOptions{})
	name, _, _ := unstructured.NestedString(got.Object, "spec", "template", "spec", "nodeClassRef", "name")
	if name != "multinode-tcp" {
		t.Errorf("TCP run should use the shared multinode-tcp node class, got %q", name)
	}

	// EFA → the pool's own class.
	dyn2 := newFakeDyn(mkPool())
	o2 := New(k8sfake.NewSimpleClientset(), database.NewMockRepo(), "pod")
	o2.SetDynamicClient(dyn2)
	if err := o2.setNodePoolNetworkMode(context.Background(), "multinode-us-east-2a", NetworkModeEFA); err != nil {
		t.Fatal(err)
	}
	got2, _ := dyn2.Resource(gvrNodePool).Get(context.Background(), "multinode-us-east-2a", metav1.GetOptions{})
	name2, _, _ := unstructured.NestedString(got2.Object, "spec", "template", "spec", "nodeClassRef", "name")
	if name2 != "multinode-us-east-2a" {
		t.Errorf("EFA run should use the base node class, got %q", name2)
	}
}

func TestSelectMultinodePool_Override(t *testing.T) {
	dyn := newFakeDyn(
		nodePoolObj("multinode-us-east-2a"),
		nodePoolObj("multinode-us-east-2b"),
	)
	o := New(k8sfake.NewSimpleClientset(), database.NewMockRepo(), "pod")
	o.SetDynamicClient(dyn)

	pools, err := o.selectMultinodePool(context.Background(), "multinode-us-east-2a")
	if err != nil {
		t.Fatal(err)
	}
	if len(pools) != 1 || pools[0] != "multinode-us-east-2a" {
		t.Errorf("override should yield exactly that pool; got %v", pools)
	}
}

func TestScaleNodePool(t *testing.T) {
	dyn := newFakeDyn(nodePoolObj("multinode-us-east-2a"))
	o := New(k8sfake.NewSimpleClientset(), database.NewMockRepo(), "pod")
	o.SetDynamicClient(dyn)

	if err := o.scaleNodePool(context.Background(), "multinode-us-east-2a", 2); err != nil {
		t.Fatal(err)
	}
	got, err := dyn.Resource(gvrNodePool).Get(context.Background(), "multinode-us-east-2a", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	replicas, _, _ := unstructured.NestedInt64(got.Object, "spec", "replicas")
	if replicas != 2 {
		t.Errorf("replicas = %d, want 2", replicas)
	}
}

func TestDeployLLMD_AppliesGraphAndTracks(t *testing.T) {
	dyn := newFakeDyn()
	client := k8sfake.NewSimpleClientset()
	o := New(client, database.NewMockRepo(), "pod")
	o.SetDynamicClient(dyn)

	cfg := distributedRunConfig("run-abc12345", 2)
	modelName := "bench-run-abc1"
	// distributedState is normally created by acquireDistributedPool.
	o.mu.Lock()
	o.distributed[modelName] = &distributedState{poolName: "multinode-us-east-2a"}
	o.mu.Unlock()

	if err := o.deployLLMD(context.Background(), "accelbench", modelName, cfg); err != nil {
		t.Fatalf("deployLLMD: %v", err)
	}

	// The LWS should exist.
	lwsGVR := crdGVRTable["leaderworkerset.x-k8s.io/v1|LeaderWorkerSet"]
	if _, err := dyn.Resource(lwsGVR).Namespace("accelbench").Get(context.Background(), modelName, metav1.GetOptions{}); err != nil {
		t.Errorf("LeaderWorkerSet not applied: %v", err)
	}

	// State should record the applied objects (4 docs: RCT, LWS, Service,
	// HTTPRoute — InferencePool deferred to PRD-58).
	o.mu.Lock()
	st := o.distributed[modelName]
	o.mu.Unlock()
	if st == nil || len(st.applied) != 4 {
		t.Fatalf("expected 4 tracked objects, got %+v", st)
	}
}

func TestDeployLLMD_TCPMode_NoEFAClaim(t *testing.T) {
	dyn := newFakeDyn()
	o := New(k8sfake.NewSimpleClientset(), database.NewMockRepo(), "pod")
	o.SetDynamicClient(dyn)

	cfg := distributedRunConfig("run-tcp12345", 2)
	cfg.NetworkMode = NetworkModeTCP
	modelName := "bench-run-tcp1"
	o.mu.Lock()
	o.distributed[modelName] = &distributedState{poolName: "multinode-us-east-2a"}
	o.mu.Unlock()

	if err := o.deployLLMD(context.Background(), "accelbench", modelName, cfg); err != nil {
		t.Fatalf("deployLLMD (tcp): %v", err)
	}
	// The ResourceClaimTemplate must have only the GPU request (no EFA).
	rctGVR := crdGVRTable["resource.k8s.io/v1|ResourceClaimTemplate"]
	rct, err := dyn.Resource(rctGVR).Namespace("accelbench").Get(context.Background(), modelName+"-devices", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get RCT: %v", err)
	}
	reqs, _, _ := unstructured.NestedSlice(rct.Object, "spec", "spec", "devices", "requests")
	if len(reqs) != 1 {
		t.Errorf("TCP mode should claim only GPU (1 request), got %d", len(reqs))
	}
}

func TestNetworkModeDefault(t *testing.T) {
	cfg := distributedRunConfig("run-1", 2)
	if cfg.networkMode() != NetworkModeEFA {
		t.Errorf("empty NetworkMode should default to efa, got %q", cfg.networkMode())
	}
	cfg.NetworkMode = NetworkModeTCP
	if cfg.networkMode() != NetworkModeTCP {
		t.Errorf("tcp should pass through, got %q", cfg.networkMode())
	}
}

func TestTeardownDistributed_DeletesGraphAndScalesIn(t *testing.T) {
	dyn := newFakeDyn(nodePoolObj("multinode-us-east-2a"))
	client := k8sfake.NewSimpleClientset()
	o := New(client, database.NewMockRepo(), "pod")
	o.SetDynamicClient(dyn)

	cfg := distributedRunConfig("run-abc12345", 2)
	modelName := "bench-run-abc1"
	// Scale out + deploy.
	if err := o.scaleNodePool(context.Background(), "multinode-us-east-2a", 2); err != nil {
		t.Fatal(err)
	}
	o.mu.Lock()
	o.distributed[modelName] = &distributedState{poolName: "multinode-us-east-2a"}
	o.mu.Unlock()
	if err := o.deployLLMD(context.Background(), "accelbench", modelName, cfg); err != nil {
		t.Fatal(err)
	}

	o.teardownDistributed(context.Background(), "accelbench", modelName)

	// LWS deleted.
	lwsGVR := crdGVRTable["leaderworkerset.x-k8s.io/v1|LeaderWorkerSet"]
	if _, err := dyn.Resource(lwsGVR).Namespace("accelbench").Get(context.Background(), modelName, metav1.GetOptions{}); err == nil {
		t.Error("LeaderWorkerSet should have been deleted")
	}
	// Pool scaled back to 0.
	got, _ := dyn.Resource(gvrNodePool).Get(context.Background(), "multinode-us-east-2a", metav1.GetOptions{})
	replicas, _, _ := unstructured.NestedInt64(got.Object, "spec", "replicas")
	if replicas != 0 {
		t.Errorf("pool replicas = %d, want 0 after teardown", replicas)
	}
	// State removed.
	o.mu.Lock()
	_, exists := o.distributed[modelName]
	o.mu.Unlock()
	if exists {
		t.Error("distributed state should be removed after teardown")
	}
}

func TestDistributedLock_Serializes(t *testing.T) {
	client := k8sfake.NewSimpleClientset()
	o := New(client, database.NewMockRepo(), "pod-A")
	ctx := context.Background()

	// pod-A acquires.
	if err := o.acquireDistributedLock(ctx, "accelbench", "bench-1", []string{"pod-A", "pod-B"}); err != nil {
		t.Fatalf("first acquire should succeed: %v", err)
	}
	// A second live-owner acquire is rejected.
	o2 := New(client, database.NewMockRepo(), "pod-B")
	if err := o2.acquireDistributedLock(ctx, "accelbench", "bench-2", []string{"pod-A", "pod-B"}); err == nil {
		t.Error("second acquire while owner live should be rejected")
	}
	// After release, it succeeds.
	o.releaseDistributedLock(ctx, "accelbench")
	if err := o2.acquireDistributedLock(ctx, "accelbench", "bench-2", []string{"pod-A", "pod-B"}); err != nil {
		t.Errorf("acquire after release should succeed: %v", err)
	}
}

func TestDistributedLock_TakesOverStale(t *testing.T) {
	client := k8sfake.NewSimpleClientset()
	dead := New(client, database.NewMockRepo(), "pod-dead")
	ctx := context.Background()
	if err := dead.acquireDistributedLock(ctx, "accelbench", "bench-1", []string{"pod-dead"}); err != nil {
		t.Fatal(err)
	}
	// New pod, and pod-dead is NOT in livePods → stale lock is taken over.
	fresh := New(client, database.NewMockRepo(), "pod-new")
	if err := fresh.acquireDistributedLock(ctx, "accelbench", "bench-2", []string{"pod-new"}); err != nil {
		t.Errorf("should take over stale lock: %v", err)
	}
	owner, exists := fresh.distributedLockOwner(ctx, "accelbench")
	if !exists || owner != "pod-new" {
		t.Errorf("lock owner = %q exists=%v, want pod-new", owner, exists)
	}
}

func TestLWSGroupReady(t *testing.T) {
	// readyReplicas signals ready.
	if !lwsGroupReady(map[string]any{"status": map[string]any{"readyReplicas": int64(1)}}) {
		t.Error("readyReplicas=1 should be ready")
	}
	// Available condition signals ready.
	if !lwsGroupReady(map[string]any{"status": map[string]any{
		"conditions": []any{map[string]any{"type": "Available", "status": "True"}},
	}}) {
		t.Error("Available=True should be ready")
	}
	// Nothing set → not ready.
	if lwsGroupReady(map[string]any{"status": map[string]any{}}) {
		t.Error("empty status should not be ready")
	}
}

func TestCountReadyDRANodes(t *testing.T) {
	readyNode := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "n1",
			Labels: map[string]string{nodePoolLabel: "multinode-us-east-2a", draNodeLabel: "true"},
		},
		Status: corev1.NodeStatus{Conditions: []corev1.NodeCondition{
			{Type: corev1.NodeReady, Status: corev1.ConditionTrue},
		}},
	}
	notReady := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "n2",
			Labels: map[string]string{nodePoolLabel: "multinode-us-east-2a", draNodeLabel: "true"},
		},
		Status: corev1.NodeStatus{Conditions: []corev1.NodeCondition{
			{Type: corev1.NodeReady, Status: corev1.ConditionFalse},
		}},
	}
	client := k8sfake.NewSimpleClientset(readyNode, notReady)
	o := New(client, database.NewMockRepo(), "pod")
	if got := o.countReadyDRANodes(context.Background(), "multinode-us-east-2a"); got != 1 {
		t.Errorf("countReadyDRANodes = %d, want 1", got)
	}
}
