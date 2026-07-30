package orchestrator

import (
	"context"
	"fmt"
	"log"
	"os"
	"sort"
	"time"

	"github.com/accelbench/accelbench/internal/manifest"
	"github.com/accelbench/accelbench/internal/runtime"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// PRD-55/56 shared-object coordinates. The Gateway + DRA device classes are
// cluster-scoped infrastructure created by Terraform; the orchestrator only
// references them. Overridable via env for installs that name them
// differently, with defaults matching the PRD-55 Terraform.
const (
	defaultGatewayName      = "accelbench-gateway"
	defaultGatewayNamespace = "envoy-gateway-system"
	defaultGPUDeviceClass   = "gpu.nvidia.com"
	defaultEFADeviceClass   = "efa.networking.k8s.aws"

	// multinode pod scheduling — matches the PRD-55 static pool taint/label.
	multinodeTaintKey  = "accelbench.io/multinode"
	multinodeTaintVal  = "true"
	draNodeSelectorKey = "accelbench.io/dra"
	draNodeSelectorVal = "true"
)

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// PRD-56 Layer 3 (serialization + pool selection): the multi-node platform
// has one shared static NodePool per AZ (multinode-<az>, PRD-55), all at
// replicas:0 at rest. A distributed run picks ONE pool, scales it out, runs,
// and scales it back in. Because the pools share the placement-group capacity
// and only one distributed run should hold the fabric at a time, runs are
// serialized: a second distributed run is rejected while one is live.
//
// The serialization lock is the cluster itself — a live LeaderWorkerSet in the
// namespace means the pool is busy. This needs no schema change (a PRD-56
// non-goal) and is inherently crash-safe: a leaked LWS is visible to the next
// run and reaped by orphan recovery, which also resets the pool to 0.

// gvrEC2NodeClass mirrors the PRD-33 API-layer coordinates; used here to read
// which multinode pools have a capacity reservation / Capacity Block attached.
var gvrEC2NodeClass = schema.GroupVersionResource{Group: "karpenter.k8s.aws", Version: "v1", Resource: "ec2nodeclasses"}

// selectMultinodePool returns the multinode NodePool names to try, in priority
// order: pools whose EC2NodeClass has a capacity reservation / Capacity Block
// attached come first (guaranteed capacity beats racing for on-demand), then
// the rest. The caller scales the first pool that provisions successfully and
// falls through to the next on insufficient capacity.
//
// When override is non-empty (a user-picked pool, PRD-57), that pool is the
// only candidate — no auto-selection, no fallthrough.
func (o *Orchestrator) selectMultinodePool(ctx context.Context, override string) ([]string, error) {
	if o.dynClient == nil {
		return nil, fmt.Errorf("dynamic client not configured")
	}
	if override != "" {
		return []string{override}, nil
	}
	pools, err := o.dynClient.Resource(gvrNodePool).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list nodepools: %w", err)
	}

	type poolRank struct {
		name     string
		reserved bool
	}
	var ranked []poolRank
	for _, p := range pools.Items {
		name := p.GetName()
		// The PRD-55 static distributed pools are named "multinode-<az>".
		if len(name) < len("multinode-") || name[:len("multinode-")] != "multinode-" {
			continue
		}
		ranked = append(ranked, poolRank{name: name, reserved: o.poolHasReservation(ctx, &p)})
	}
	if len(ranked) == 0 {
		return nil, fmt.Errorf("no multinode NodePools found (is enable_multinode set in Terraform?)")
	}

	// Reserved pools first; stable by name within each group for determinism.
	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].reserved != ranked[j].reserved {
			return ranked[i].reserved // reserved (true) sorts before unreserved
		}
		return ranked[i].name < ranked[j].name
	})

	names := make([]string, 0, len(ranked))
	for _, r := range ranked {
		names = append(names, r.name)
	}
	log.Printf("[distributed] multinode pool preference order: %v", names)
	return names, nil
}

// poolHasReservation reports whether the NodePool's referenced EC2NodeClass has
// a capacity reservation / Capacity Block attached (a non-empty
// capacityReservationSelectorTerms), which the operator sets from the
// Configuration page (PRD-33). Best-effort: on any lookup error, treat the
// pool as unreserved so it still ranks (just lower).
func (o *Orchestrator) poolHasReservation(ctx context.Context, pool interface{ GetName() string }) bool {
	// The NodePool references its EC2NodeClass by name under
	// spec.template.spec.nodeClassRef.name. Our static pools share the pool's
	// name suffix ("multinode-<az>"), so read the class of the same name.
	name := pool.GetName()
	nc, err := o.dynClient.Resource(gvrEC2NodeClass).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return false
	}
	terms, found, _ := unstructured.NestedSlice(nc.Object, "spec", "capacityReservationSelectorTerms")
	return found && len(terms) > 0
}

// acquireDistributedPool serializes on the shared multi-node pool, picks a
// pool (preferring reserved capacity, honoring an override), scales it out to
// NodeCount, and waits for the nodes to be Ready + DRA-labeled. It records the
// scaled pool in the run's distributedState so teardown returns it to 0. On
// any failure the pool it scaled is returned to 0 before erroring, so a failed
// acquire never leaks nodes.
func (o *Orchestrator) acquireDistributedPool(ctx context.Context, ns, modelName string, cfg RunConfig) error {
	// Serialize: one distributed run at a time on the shared pool. The lock
	// ConfigMap is held for the WHOLE run (created before the first scale-out),
	// so the provisioning window — nodes up but no LWS yet — is covered. A
	// stale lock (owner pod dead) is taken over.
	live, err := o.repo.LiveAPIPods(ctx, heartbeatTTL)
	if err != nil {
		log.Printf("[%s] distributed lock: live-pods lookup failed, treating existing lock as held: %v", cfg.RunID[:8], err)
	}
	if err := o.acquireDistributedLock(ctx, ns, modelName, live); err != nil {
		return err
	}

	pools, err := o.selectMultinodePool(ctx, cfg.NodePoolOverride)
	if err != nil {
		o.releaseDistributedLock(ctx, ns)
		return err
	}

	// Record state up front so teardown fires even if we fail mid-scale.
	// Keyed by modelName so teardown(ns, modelName, ...) can find it.
	st := &distributedState{}
	o.mu.Lock()
	o.distributed[modelName] = st
	o.mu.Unlock()

	var lastErr error
	for _, pool := range pools {
		if err := o.scaleNodePool(ctx, pool, cfg.NodeCount); err != nil {
			lastErr = err
			continue
		}
		// Claim the pool for teardown the moment it's scaled — even if the
		// wait below fails, teardown must reset THIS pool to 0.
		o.mu.Lock()
		st.poolName = pool
		o.mu.Unlock()

		if err := o.waitForNodes(ctx, pool, cfg.NodeCount); err != nil {
			// Insufficient capacity (or timeout) in this AZ: scale back to 0
			// and try the next pool. An explicit override has only one
			// candidate, so this just surfaces the error.
			log.Printf("[%s] pool %s did not provision (%v); scaling back and trying next",
				cfg.RunID[:8], pool, err)
			if serr := o.scaleNodePool(context.WithoutCancel(ctx), pool, 0); serr != nil {
				log.Printf("[%s] warning: scale %s back to 0: %v", cfg.RunID[:8], pool, serr)
			}
			o.mu.Lock()
			st.poolName = ""
			o.mu.Unlock()
			lastErr = err
			continue
		}
		log.Printf("[%s] distributed pool %s ready with %d nodes", cfg.RunID[:8], pool, cfg.NodeCount)
		return nil
	}
	// Every candidate pool failed — release the lock (nothing is scaled out;
	// each failed pool was already reset to 0 above) so the next run can try.
	o.releaseDistributedLock(ctx, ns)
	return fmt.Errorf("no multinode pool could provide %d nodes: %w", cfg.NodeCount, lastErr)
}

// deployLLMD renders the multi-node llm-d object graph from the run's topology
// and applies it via the dynamic client, tracking every object for teardown
// (PRD-56 Layer 4). Called from deployModel when cfg.IsDistributed().
func (o *Orchestrator) deployLLMD(ctx context.Context, ns, name string, cfg RunConfig) error {
	rt, err := runtime.Get(cfg.Request.Framework)
	if err != nil {
		return err
	}

	// S3-backed models load via the Run:ai streamer, same as single-node.
	var modelS3URI string
	useRunai := false
	if cfg.Request.ModelS3URI != "" {
		modelS3URI = cfg.Request.ModelS3URI
		useRunai = true
	}

	gpusPerNode := cfg.GPUsPerNode
	if gpusPerNode <= 0 {
		gpusPerNode = cfg.InstanceType.AcceleratorCount
	}

	image := rt.ResolveImageOverride()
	if image == "" {
		image = rt.DefaultImage(cfg.Request.FrameworkVersion, "")
	}

	command, args := rt.BuildArgs(runtime.ContainerParams{
		ModelHfID:              cfg.Request.ModelHfID,
		ModelS3URI:             modelS3URI,
		UseRunaiStreamer:       useRunai,
		TensorParallelDegree:   cfg.Request.TensorParallelDegree,
		PipelineParallelDegree: cfg.PipelineParallelDegree,
		NodeCount:              cfg.NodeCount,
		GPUsPerNode:            gpusPerNode,
		MaxModelLen:            cfg.Request.MaxModelLen,
		MaxNumBatchedTokens:    cfg.Request.MaxNumBatchedTokens,
		KVCacheDtype:           cfg.Request.KVCacheDtype,
		Quantization:           derefStr(cfg.Request.Quantization),
		StreamerConcurrency:    cfg.Request.StreamerConcurrency,
		AcceleratorName:        cfg.InstanceType.AcceleratorName,
	})

	var modelServiceAccount string
	if useRunai {
		modelServiceAccount = "accelbench-model"
	}

	// Per-pod CPU/memory headroom (GPUs come via the DRA claim, not requests).
	cpuReq := fmt.Sprintf("%d", max(1, cfg.InstanceType.VCPUs*3/4))
	memReq := fmt.Sprintf("%dGi", max(1, cfg.InstanceType.MemoryGiB*85/100))

	// EFA devices per node: one per GPU is the common p5 alignment. Zero in
	// TCP mode — no EFA ResourceClaimTemplate is rendered and NCCL falls back
	// to sockets (the user opted out of EFA for this run).
	efaPerNode := gpusPerNode
	if cfg.networkMode() == NetworkModeTCP {
		efaPerNode = 0
	}

	yamlStr, err := manifest.RenderLLMDDeployment(manifest.LLMDDeploymentParams{
		Name:                   name,
		Namespace:              ns,
		Image:                  image,
		Command:                command,
		Args:                   args,
		ContainerName:          rt.ContainerName(),
		ModelHfID:              cfg.Request.ModelHfID,
		HfToken:                o.resolveHFToken(ctx, cfg.Request.HfToken),
		ModelServiceAccount:    modelServiceAccount,
		NodeCount:              cfg.NodeCount,
		TensorParallelDegree:   cfg.Request.TensorParallelDegree,
		PipelineParallelDegree: cfg.PipelineParallelDegree,
		GPUsPerNode:            gpusPerNode,
		CPURequest:             cpuReq,
		MemoryRequest:          memReq,
		NetworkMode:            cfg.networkMode(),
		GPUDeviceClass:         envOr("DRA_GPU_DEVICE_CLASS", defaultGPUDeviceClass),
		EFADeviceClass:         envOr("DRA_EFA_DEVICE_CLASS", defaultEFADeviceClass),
		EFAPerNode:             efaPerNode,
		GatewayName:            envOr("LLMD_GATEWAY_NAME", defaultGatewayName),
		GatewayNamespace:       envOr("LLMD_GATEWAY_NAMESPACE", defaultGatewayNamespace),
		MultiNodeTaintKey:      multinodeTaintKey,
		MultiNodeTaintValue:    multinodeTaintVal,
		DRANodeSelectorKey:     draNodeSelectorKey,
		DRANodeSelectorVal:     draNodeSelectorVal,
	})
	if err != nil {
		return fmt.Errorf("render llm-d manifest set: %w", err)
	}

	applied, err := o.applyManifestSet(ctx, ns, yamlStr)
	// Record whatever was applied (even on partial failure) so teardown cleans
	// it up. distributedState was created in acquireDistributedPool, keyed by
	// modelName (== name here).
	o.mu.Lock()
	if st := o.distributed[name]; st != nil {
		st.applied = applied
	}
	o.mu.Unlock()
	if err != nil {
		return fmt.Errorf("apply llm-d manifest set: %w", err)
	}
	log.Printf("[%s] applied llm-d object graph: %d objects", cfg.RunID[:8], len(applied))
	return nil
}

// distributedReadinessTimeout is the LWS-group readiness budget. Larger than
// the single-node budget because multi-node model load (weights across nodes +
// fabric bring-up) is slower; node provisioning is NOT included here (that's
// acquireDistributedPool's separate wait).
const distributedReadinessTimeout = 45 * time.Minute

// waitForLWSReady polls the LeaderWorkerSet's status until the group reports
// ready, scanning for OOM events on group pods along the way (PRD-56 Layer 5).
func (o *Orchestrator) waitForLWSReady(ctx context.Context, ns, name string, cfg RunConfig) error {
	gvr := crdGVRTable["leaderworkerset.x-k8s.io/v1|LeaderWorkerSet"]
	deadline := time.Now().Add(distributedReadinessTimeout)
	for time.Now().Before(deadline) {
		lws, err := o.dynClient.Resource(gvr).Namespace(ns).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			// The object may not be visible immediately after apply; retry.
			log.Printf("[%s] get LWS %s: %v", cfg.RunID[:8], name, err)
		} else if lwsGroupReady(lws.Object) {
			log.Printf("[%s] LeaderWorkerSet %s ready", cfg.RunID[:8], name)
			return nil
		}

		// OOM scan across all group pods (leader + workers).
		pods, _ := o.client.CoreV1().Pods(ns).List(ctx, metav1.ListOptions{
			LabelSelector: fmt.Sprintf("app.kubernetes.io/name=%s", name),
		})
		for _, pod := range pods.Items {
			events, err := o.oomDetector.CheckPod(ctx, pod.Name)
			if err == nil && len(events) > 0 {
				for _, ev := range events {
					o.recordOOMEvent(ctx, cfg, ev)
				}
				return fmt.Errorf("OOM detected: %s", events[0].Message)
			}
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(readinessPoll):
		}
	}
	return fmt.Errorf("LeaderWorkerSet %s not ready after %v", name, distributedReadinessTimeout)
}

// lwsGroupReady reports whether an LWS group is fully ready. LWS surfaces
// status.readyReplicas (ready GROUPS) and, in recent versions, a
// "Available" condition. We treat readyReplicas>=1 (our single group) as the
// ready signal, falling back to the Available condition when present.
func lwsGroupReady(obj map[string]any) bool {
	if ready, found, _ := unstructured.NestedInt64(obj, "status", "readyReplicas"); found && ready >= 1 {
		return true
	}
	conds, found, _ := unstructured.NestedSlice(obj, "status", "conditions")
	if !found {
		return false
	}
	for _, c := range conds {
		m, ok := c.(map[string]any)
		if !ok {
			continue
		}
		if m["type"] == "Available" && m["status"] == "True" {
			return true
		}
	}
	return false
}

// llmdServingNodeIPs returns the HostIP of every running llm-d group pod, so
// the GPU scraper can fan out to each node's DCGM exporter (PRD-56 Layer 5).
func (o *Orchestrator) llmdServingNodeIPs(ctx context.Context, ns, name string) []string {
	pods, err := o.client.CoreV1().Pods(ns).List(ctx, metav1.ListOptions{
		LabelSelector: fmt.Sprintf("app.kubernetes.io/name=%s", name),
	})
	if err != nil {
		return nil
	}
	seen := map[string]bool{}
	var ips []string
	for _, pod := range pods.Items {
		if pod.Status.Phase == corev1.PodRunning && pod.Status.HostIP != "" && !seen[pod.Status.HostIP] {
			seen[pod.Status.HostIP] = true
			ips = append(ips, pod.Status.HostIP)
		}
	}
	return ips
}

// gatewayLoadgenTarget returns the (host, port) the loadgen should target for a
// distributed run: the shared Envoy AI Gateway's Service by in-cluster DNS. The
// gateway is ClusterIP; the OpenAI path is served through the HTTPRoute the
// deploy created (PRD-56 Layer 5).
//
// Envoy Gateway does NOT expose the Gateway object as a Service directly — it
// provisions a backing Service with a generated name
// ("envoy-<ns>-<gw>-<hash>") labeled with the owning gateway. We resolve that
// Service by label at runtime so we hit the right ClusterIP DNS name. Falls
// back to an explicit LLMD_GATEWAY_HOST override, then to the generated-name
// pattern's namespace default, if the lookup can't find it.
func (o *Orchestrator) gatewayLoadgenTarget(ctx context.Context) (string, int) {
	gwName := envOr("LLMD_GATEWAY_NAME", defaultGatewayName)
	gwNS := envOr("LLMD_GATEWAY_NAMESPACE", defaultGatewayNamespace)
	if host := os.Getenv("LLMD_GATEWAY_HOST"); host != "" {
		return host, 80
	}
	svcs, err := o.client.CoreV1().Services(gwNS).List(ctx, metav1.ListOptions{
		LabelSelector: fmt.Sprintf("gateway.envoyproxy.io/owning-gateway-name=%s", gwName),
	})
	if err == nil && len(svcs.Items) > 0 {
		svc := svcs.Items[0]
		return fmt.Sprintf("%s.%s.svc.cluster.local", svc.Name, gwNS), 80
	}
	// Last resort: the Gateway name as a DNS host (works only if an operator
	// created a matching Service). Logged so a failed loadgen is diagnosable.
	log.Printf("[distributed] warning: could not resolve Envoy Service for gateway %s/%s; falling back to %s",
		gwNS, gwName, gwName)
	return fmt.Sprintf("%s.%s.svc.cluster.local", gwName, gwNS), 80
}

// teardownDistributed deletes the applied llm-d object graph and scales the
// run's NodePool back to 0 (PRD-56 Layer 5). Keyed by modelName. Order matters:
// delete workloads first so nodes drain before scale-in. Idempotent and
// best-effort; a no-op for single-node runs (no state recorded).
func (o *Orchestrator) teardownDistributed(ctx context.Context, ns, modelName string) {
	o.mu.Lock()
	st := o.distributed[modelName]
	delete(o.distributed, modelName)
	o.mu.Unlock()
	if st == nil {
		return
	}
	// Release the serialization lock last-thing so the next distributed run
	// can't start until this one's pool is torn down and scaled in.
	defer o.releaseDistributedLock(ctx, ns)

	// Delete in reverse apply order (route → pool → service → LWS → claims)
	// so the gateway stops routing before the pods disappear.
	for i := len(st.applied) - 1; i >= 0; i-- {
		if err := o.deleteUnstructured(ctx, ns, st.applied[i]); err != nil {
			log.Printf("[distributed] delete %s/%s: %v", st.applied[i].gvr.Resource, st.applied[i].name, err)
		}
	}

	// Scale the pool back to 0 — the reaping action. Leaked p5 nodes are real
	// money, so this must run even if the deletes above failed.
	if st.poolName != "" {
		if err := o.scaleNodePool(ctx, st.poolName, 0); err != nil {
			log.Printf("[distributed] scale %s back to 0: %v", st.poolName, err)
		}
	}
}
