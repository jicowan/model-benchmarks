package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/client-go/dynamic"
)

// PRD-56: applying and deleting the custom resources a multi-node llm-d
// deployment needs (which the typed clientset can't create) plus scaling the
// static Karpenter NodePool.
//
// We render our own manifests, so the set of custom kinds is fixed and known.
// A static apiVersion/Kind → GroupVersionResource table is therefore simpler,
// more testable, and avoids in-pod discovery calls that a full RESTMapper
// would require. If a rendered doc ever carries a kind not in this table,
// applyUnstructured fails loudly rather than silently dropping it.
var crdGVRTable = map[string]schema.GroupVersionResource{
	"leaderworkerset.x-k8s.io/v1|LeaderWorkerSet": {Group: "leaderworkerset.x-k8s.io", Version: "v1", Resource: "leaderworkersets"},
	"gateway.networking.k8s.io/v1|HTTPRoute":      {Group: "gateway.networking.k8s.io", Version: "v1", Resource: "httproutes"},
	"resource.k8s.io/v1|ResourceClaimTemplate":    {Group: "resource.k8s.io", Version: "v1", Resource: "resourceclaimtemplates"},
	// Core Service is applied+tracked through the dynamic client too so the
	// whole llm-d object graph tears down uniformly (PRD-56 Layer 2).
	"v1|Service": {Group: "", Version: "v1", Resource: "services"},

	// PRD-58: the prefill/decode disaggregated object graph adds the
	// InferencePool + Endpoint Picker (EPP) and the two role Deployments. All
	// are applied+tracked through the dynamic client so teardown deletes the
	// full graph uniformly. ClusterRole/ClusterRoleBinding are cluster-scoped
	// (see clusterScopedKinds below — they're applied/deleted without a
	// namespace, which a real API server requires).
	"inference.networking.k8s.io/v1|InferencePool": {Group: "inference.networking.k8s.io", Version: "v1", Resource: "inferencepools"},
	"apps/v1|Deployment":                           {Group: "apps", Version: "v1", Resource: "deployments"},
	"v1|ConfigMap":                                 {Group: "", Version: "v1", Resource: "configmaps"},
	"v1|ServiceAccount":                            {Group: "", Version: "v1", Resource: "serviceaccounts"},
	"rbac.authorization.k8s.io/v1|Role":            {Group: "rbac.authorization.k8s.io", Version: "v1", Resource: "roles"},
	"rbac.authorization.k8s.io/v1|RoleBinding":     {Group: "rbac.authorization.k8s.io", Version: "v1", Resource: "rolebindings"},
	"rbac.authorization.k8s.io/v1|ClusterRole":        {Group: "rbac.authorization.k8s.io", Version: "v1", Resource: "clusterroles"},
	"rbac.authorization.k8s.io/v1|ClusterRoleBinding": {Group: "rbac.authorization.k8s.io", Version: "v1", Resource: "clusterrolebindings"},
}

// clusterScopedKinds are applied/deleted WITHOUT a namespace even when the
// manifest set is otherwise namespaced (PRD-58 EPP RBAC). Passing a namespace
// for these to the dynamic client is rejected by a real API server.
var clusterScopedKinds = map[string]bool{
	"rbac.authorization.k8s.io/v1|ClusterRole":        true,
	"rbac.authorization.k8s.io/v1|ClusterRoleBinding": true,
}

// gvrNodePool is the Karpenter NodePool resource (PRD-33 uses the same
// coordinates in the API layer). The orchestrator patches spec.replicas on
// the static multi-node pool (PRD-56 Layer 3).
var gvrNodePool = schema.GroupVersionResource{Group: "karpenter.sh", Version: "v1", Resource: "nodepools"}

// appliedObject records a single custom resource applied for a run so teardown
// can delete exactly what was created (PRD-56 Layer 2). clusterScoped marks
// resources (ClusterRole/ClusterRoleBinding) that must be addressed without a
// namespace (PRD-58).
type appliedObject struct {
	gvr          schema.GroupVersionResource
	name         string
	clusterScoped bool
}

// resolveCRDGVR maps a document's apiVersion+kind to its GroupVersionResource
// using the static table. Returns an error for unknown kinds.
func resolveCRDGVR(apiVersion, kind string) (schema.GroupVersionResource, error) {
	gvr, ok := crdGVRTable[apiVersion+"|"+kind]
	if !ok {
		return schema.GroupVersionResource{}, fmt.Errorf("no GVR mapping for %s/%s (rendered a kind the orchestrator can't apply)", apiVersion, kind)
	}
	return gvr, nil
}

// applyUnstructured creates one custom-resource document via the dynamic
// client (falling back to update on AlreadyExists) and returns its GVR+name
// for teardown tracking. Create-or-update keeps re-apply idempotent — a
// retried deploy of the same run reconciles rather than erroring.
func (o *Orchestrator) applyUnstructured(ctx context.Context, ns, docJSON string) (appliedObject, error) {
	if o.dynClient == nil {
		return appliedObject{}, fmt.Errorf("dynamic client not configured; multi-node deploys unavailable")
	}
	obj := &unstructured.Unstructured{}
	if err := obj.UnmarshalJSON([]byte(docJSON)); err != nil {
		return appliedObject{}, fmt.Errorf("decode custom resource: %w", err)
	}
	gvr, err := resolveCRDGVR(obj.GetAPIVersion(), obj.GetKind())
	if err != nil {
		return appliedObject{}, err
	}
	name := obj.GetName()
	clusterScoped := clusterScopedKinds[obj.GetAPIVersion()+"|"+obj.GetKind()]
	// Cluster-scoped resources (ClusterRole/ClusterRoleBinding) must NOT be
	// addressed with a namespace — a real API server rejects that.
	var ri dynamic.ResourceInterface
	if clusterScoped {
		ri = o.dynClient.Resource(gvr)
	} else {
		ri = o.dynClient.Resource(gvr).Namespace(ns)
	}

	_, err = ri.Create(ctx, obj, metav1.CreateOptions{FieldManager: "accelbench-orchestrator"})
	if apierrors.IsAlreadyExists(err) {
		// Reconcile an existing object: fetch its resourceVersion and update.
		cur, gerr := ri.Get(ctx, name, metav1.GetOptions{})
		if gerr != nil {
			return appliedObject{}, fmt.Errorf("get existing %s %q: %w", obj.GetKind(), name, gerr)
		}
		obj.SetResourceVersion(cur.GetResourceVersion())
		if _, uerr := ri.Update(ctx, obj, metav1.UpdateOptions{FieldManager: "accelbench-orchestrator"}); uerr != nil {
			return appliedObject{}, fmt.Errorf("update %s %q: %w", obj.GetKind(), name, uerr)
		}
	} else if err != nil {
		return appliedObject{}, fmt.Errorf("apply %s/%s %q: %w", obj.GetAPIVersion(), obj.GetKind(), name, err)
	}
	return appliedObject{gvr: gvr, name: name, clusterScoped: clusterScoped}, nil
}

// applyManifestSet decodes a multi-document YAML string and server-side-applies
// every document via the dynamic client, returning the tracked objects in
// apply order (PRD-56 Layer 2). On the first apply failure it returns the
// objects applied so far alongside the error so the caller can tear them down.
func (o *Orchestrator) applyManifestSet(ctx context.Context, ns, yamlStr string) ([]appliedObject, error) {
	decoder := yaml.NewYAMLOrJSONDecoder(io.NopCloser(strings.NewReader(yamlStr)), 4096)
	var applied []appliedObject
	for {
		var raw json.RawMessage
		if err := decoder.Decode(&raw); err != nil {
			if err == io.EOF {
				break
			}
			return applied, fmt.Errorf("decode manifest set: %w", err)
		}
		if len(raw) == 0 {
			continue
		}
		obj, err := o.applyUnstructured(ctx, ns, string(raw))
		if err != nil {
			return applied, err
		}
		applied = append(applied, obj)
	}
	return applied, nil
}

// deleteUnstructured deletes one tracked custom resource. Best-effort:
// NotFound is not an error (teardown is idempotent and may run twice).
func (o *Orchestrator) deleteUnstructured(ctx context.Context, ns string, obj appliedObject) error {
	if o.dynClient == nil {
		return nil
	}
	propagation := metav1.DeletePropagationBackground
	var ri dynamic.ResourceInterface
	if obj.clusterScoped {
		ri = o.dynClient.Resource(obj.gvr)
	} else {
		ri = o.dynClient.Resource(obj.gvr).Namespace(ns)
	}
	err := ri.Delete(ctx, obj.name, metav1.DeleteOptions{
		PropagationPolicy: &propagation,
	})
	if err != nil && !strings.Contains(strings.ToLower(err.Error()), "not found") {
		return err
	}
	return nil
}
