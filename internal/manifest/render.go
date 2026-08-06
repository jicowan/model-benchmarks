package manifest

import (
	"bytes"
	"embed"
	"fmt"
	"strings"
	"text/template"
)

//go:embed templates/*.yaml.tmpl
var templateFS embed.FS

// PDBothRoleLabel is the canonical llm-d.ai/role WIRE VALUE the template emits
// for the co-located "both" pool (PRD-63). llm-d-inference-scheduler v0.9.0
// documents "both" as a deprecated alias of "prefill-decode" (roles.go), so we
// render the canonical value to survive a future EPP dropping the alias. The
// orchestrator's PD metric scraper matches pod labels against this same
// constant; the template hardcodes the literal and TestBothRoleLabelMatchesConst
// guards the two from drifting. (Our INTERNAL role key + object-name suffix stay
// "both" — this constant is only the rendered/observed label value.)
const PDBothRoleLabel = "prefill-decode"

var templates *template.Template

func init() {
	var err error
	templates, err = template.New("").Funcs(template.FuncMap{
		"sub":        func(a, b int) int { return a - b },
		"div":        func(a, b int) int { return a / b },
		// gibBytes converts a GiB count to bytes for env vars like
		// RUNAI_STREAMER_MEMORY_LIMIT that expect a raw byte count.
		"gibBytes":   func(gib int) int64 { return int64(gib) * 1024 * 1024 * 1024 },
		// toYAMLStringList renders a Go []string as an inline YAML list
		// e.g. ["python3"] → '["python3"]'
		"toYAMLStringList": func(ss []string) string {
			if len(ss) == 0 {
				return "[]"
			}
			var b strings.Builder
			b.WriteString("[")
			for i, s := range ss {
				if i > 0 {
					b.WriteString(", ")
				}
				b.WriteString("\"")
				b.WriteString(s)
				b.WriteString("\"")
			}
			b.WriteString("]")
			return b.String()
		},
		// yamlQuote quotes a string for use as a YAML scalar value.
		// Uses single quotes for values containing double quotes (e.g. JSON),
		// double quotes for everything else.
		"yamlQuote": func(s string) string {
			if strings.Contains(s, "\"") {
				return "'" + s + "'"
			}
			return "\"" + s + "\""
		},
		// dict builds a map from alternating key/value args, so a nested
		// template can receive the outer params plus extra fields (PRD-56
		// uses this to pass the LWS pod role — leader vs worker — into the
		// shared pod-spec define).
		"dict": func(kv ...any) (map[string]any, error) {
			if len(kv)%2 != 0 {
				return nil, fmt.Errorf("dict: odd number of arguments (%d)", len(kv))
			}
			m := make(map[string]any, len(kv)/2)
			for i := 0; i < len(kv); i += 2 {
				key, ok := kv[i].(string)
				if !ok {
					return nil, fmt.Errorf("dict: key %d is not a string", i)
				}
				m[key] = kv[i+1]
			}
			return m, nil
		},
	}).ParseFS(templateFS, "templates/*.yaml.tmpl")
	if err != nil {
		panic(fmt.Sprintf("parse manifest templates: %v", err))
	}
}

// ModelDeploymentParams holds values for rendering the model Deployment + Service.
type ModelDeploymentParams struct {
	Name                 string
	Namespace            string
	ModelHfID            string
	HfToken              string
	Framework            string // "vllm", "vllm-neuron", or "sglang"
	FrameworkVersion     string
	TensorParallelDegree int
	Quantization         string // "fp16", "int8", "int4", or ""
	AcceleratorType      string // "gpu" or "neuron"
	AcceleratorCount     int
	AcceleratorMemoryGiB int
	InstanceTypeName     string // e.g. "g5.48xlarge", "inf2.xlarge"
	InstanceFamily       string // e.g. "p5", "inf2"
	MaxModelLen          int    // 0 = auto-detect from model config
	MaxNumBatchedTokens  int    // 0 = vLLM default; emits --max-num-batched-tokens when > 0
	MaxNumSeqs           int    // 0 = vLLM default; emits --max-num-seqs when > 0
	KVCacheDtype         string // empty = vLLM default (matches compute dtype); emits --kv-cache-dtype when set (e.g. "fp8")
	CPURequest           string
	MemoryRequest        string
	ModelS3URI           string // s3://bucket/models/org/model (empty = use HF)
	UseRunaiStreamer      bool   // true = --load-format runai_streamer
	ModelServiceAccount   string // K8s service account for S3 access
	StreamerConcurrency   int    // runai_streamer concurrency (default 16)
	PullThroughRegistry   string // ECR pull-through cache host (empty = direct Docker Hub)
	// PRD-49: full vLLM image URI override. When non-empty, used verbatim
	// as the model container image and the PullThroughRegistry +
	// FrameworkVersion template path is skipped. Plumbed from the
	// VLLM_IMAGE env var on the API pod; see internal/orchestrator/versions.go.
	VLLMImageOverride     string
	// SGLangImageOverride: full SGLang image URI override. Plumbed from
	// the SGLANG_IMAGE env var. Mirrors VLLMImageOverride for SGLang
	// deployments; ignored unless Framework == "sglang".
	SGLangImageOverride   string
	// PRD-50: RUNAI_STREAMER_MEMORY_LIMIT env var (in GiB). Caps the
	// streamer's shared CPU buffer during weight load. 0 = emit no env
	// var, inheriting the upstream 40 GB default.
	StreamerMemoryLimitGiB int

	// Runtime interface fields: when Image is non-empty, the template uses
	// these pre-computed values instead of the legacy framework conditionals.
	RuntimeContainerName string   // k8s container name (e.g. "vllm", "sglang")
	RuntimeImage         string   // fully resolved container image URI
	RuntimeCommand       []string // container command (nil = use image entrypoint)
	RuntimeArgs          []string // container args
}

// LoadgenJobParams holds values for rendering the load generator Job.
// Result storage is now configured inside the inference-perf YAML (see
// InferencePerfConfigParams.StorageBucket); inference-perf uploads to S3
// natively via boto3, so there's no upload sidecar here.
type LoadgenJobParams struct {
	Name               string
	Namespace          string
	InferencePerfImage string // inference-perf container image
	ConfigMapName      string // ConfigMap containing inference-perf config
	AWSRegion          string // AWS region; exported to the container so boto3 signs SigV4 correctly
	HfToken            string // HuggingFace token for downloading datasets (sharegpt, cnn_dailymail)

	// Pod resources. Empty strings fall back to the historical defaults
	// (2/4 CPU request/limit, 4/8 GiB memory request/limit). Callers
	// that want the requests to scale with num_workers should compute
	// them via orchestrator.loadgenResources and pass the strings in.
	CPURequest    string
	CPULimit      string
	MemoryRequest string
	MemoryLimit   string
}

// CacheJobParams holds values for rendering the model cache Job, which
// streams a HuggingFace model into the S3 cache bucket.
type CacheJobParams struct {
	Name       string
	Namespace  string
	CacheID    string
	CacheImage string
	ModelHfID  string
	HfRevision string
	ModelPath  string // org/model-name (derived from HfID)
	S3Bucket   string
	HfToken    string
	AWSRegion  string
}

// LLMDDeploymentParams holds values for rendering a multi-node llm-d
// deployment (PRD-56): a LeaderWorkerSet spanning NodeCount GPU nodes, an
// InferencePool selecting its pods, an HTTPRoute binding the pool to the
// Envoy AI Gateway, and the DRA/EFA ResourceClaimTemplates. The single
// object graph is applied by the orchestrator via the dynamic client and torn
// down together.
type LLMDDeploymentParams struct {
	Name      string
	Namespace string

	// Container image + serve args. ServeArgs is the model positional arg plus
	// static tuning flags (from the llm-d Runtime's BuildArgs) — WITHOUT the
	// multi-node coordination flags, which the template appends from LWS
	// runtime env. EVERY pod (leader + workers) runs the same `vllm serve`; the
	// data-parallel supervisor coordinates them via --data-parallel-address,
	// mirroring llm-d's guides/wide-ep-lws launch (NOT Ray).
	Image         string
	ServeArgs     []string // shell-quoted and appended after the DP flags
	ContainerName string   // k8s container name (e.g. "vllm")
	ModelHfID        string
	HfToken          string
	ModelServiceAccount string // K8s service account for S3 access (empty = default SA)

	// Topology.
	NodeCount              int // LWS group size (leader + workers)
	TensorParallelDegree   int // GPUs per node (within-node parallelism)
	PipelineParallelDegree int // shards across nodes
	GPUsPerNode            int // accelerators claimed per pod

	// Per-pod resource requests (CPU/memory); GPUs are claimed via the DRA
	// ResourceClaimTemplate, not the nvidia.com/gpu extended resource.
	CPURequest    string
	MemoryRequest string

	// NetworkMode selects the cross-node collective fabric (PRD-56):
	//   "efa" (default, preferred) — claim EFA devices + libfabric efa provider.
	//   "tcp"                      — NCCL over plain sockets, no EFA claim.
	// When "tcp", EFAPerNode is ignored (no EFA ResourceClaimTemplate rendered)
	// and the pod env drops the EFA vars in favor of NCCL_NET=Socket.
	NetworkMode string

	// DRA/EFA wiring. The GPU claim and the EFA claim are PCIe-root-aligned
	// so NCCL gets the NIC on the same PCIe switch as the GPUs.
	GPUDeviceClass string // e.g. "gpu.nvidia.com"
	EFADeviceClass string // e.g. "efa.networking.k8s.aws"
	EFAPerNode     int    // EFA devices claimed per pod (ignored when NetworkMode == "tcp")

	// Gateway binding. The route attaches to the shared Envoy AI Gateway
	// (PRD-55) which is a ClusterIP Service; the loadgen targets it by DNS.
	GatewayName      string
	GatewayNamespace string

	// Scheduling: the PRD-55 static multi-node pool. Pods tolerate the
	// dedicated taint and select the DRA-ready label.
	MultiNodeTaintKey   string // e.g. "accelbench.io/multinode"
	MultiNodeTaintValue string // e.g. "true"
	DRANodeSelectorKey  string // e.g. "accelbench.io/dra"
	DRANodeSelectorVal  string // e.g. "true"
	// InstanceTypeName adds a node.kubernetes.io/instance-type nodeSelector to
	// every group pod (belt-and-suspenders — the static pool's own requirements,
	// set per-run by the orchestrator, are what actually drive provisioning; a
	// static pool ignores pod constraints per the Karpenter docs). Empty ⇒ none.
	InstanceTypeName string
}

// RenderLLMDDeployment renders the multi-node llm-d object graph as a
// multi-document YAML string (PRD-56). Documents: ResourceClaimTemplate(s),
// LeaderWorkerSet, Service, InferencePool, HTTPRoute.
func RenderLLMDDeployment(params LLMDDeploymentParams) (string, error) {
	return renderTemplate("llmd-deployment.yaml.tmpl", params)
}

// LLMDDisaggregatedParams holds values for rendering a PREFILL/DECODE
// disaggregated llm-d object graph (PRD-58): two independently-scaled pod
// groups (prefill + decode) wired for NIXL KV transfer, fronted by the Gateway
// API InferencePool + Endpoint Picker (EPP) for KV/role/load-aware routing.
// Mirrors the hand-validated reference
// (terraform/manifests/pd-layer1-epp-reference.yaml).
type LLMDDisaggregatedParams struct {
	Name      string
	Namespace string

	// Model container image (PD uses the upstream vllm/vllm-openai image, which
	// ships the cu13 NIXL modules — NOT the llm-d-aws image) + serve args (model
	// positional + static tuning flags; per-role TP/port are appended by the
	// template). ModelLabel is a DNS-safe form of the model id used for the
	// InferencePool selector (llm-d.ai/model).
	Image         string
	// ServeArgs is the shared/default arg set. PrefillServeArgs / DecodeServeArgs
	// (PRD-64) are the per-role arg sets — identical to ServeArgs except the
	// scheduler knob (--max-num-batched-tokens) may differ per role. When the
	// per-role sets are empty the template falls back to ServeArgs, so a run
	// with no per-role override renders byte-identically to pre-PRD-64.
	ServeArgs        []string
	PrefillServeArgs []string
	DecodeServeArgs  []string
	// BothServeArgs (PRD-63) is the per-role arg set for the co-located "both"
	// role — identical to ServeArgs except the scheduler knob may differ. Nil ⇒
	// the template falls back to the shared ServeArgs (byte-identical render).
	BothServeArgs []string
	ContainerName string
	ModelHfID     string
	ModelLabel    string
	HfToken       string
	ModelServiceAccount string
	// PRD-65 Layer 3: RUNAI_STREAMER_MEMORY_LIMIT env (GiB) on every model
	// container when > 0. Caps the streamer's shared CPU buffer during weight
	// load, mirroring the single-node model-deployment template. 0 ⇒ emit no
	// env var (inherit the upstream default). Only meaningful when the run
	// streams from S3 (ServeArgs carry --load-format runai_streamer).
	StreamerMemoryLimitGiB int
	// InstanceTypeName adds a node.kubernetes.io/instance-type nodeSelector to
	// every serving pod. NOTE: a STATIC NodePool provisions from its OWN template
	// requirements, ignoring pods (Karpenter docs), so this selector does NOT
	// drive provisioning — the orchestrator sets the pool's instance-type per run
	// (setNodePoolInstanceType) for that. This is a belt-and-suspenders guard so
	// pods only bind a node of the selected type (never accidentally a leftover
	// node of a different type). Empty ⇒ no selector.
	InstanceTypeName string

	// Per-role topology. TP is within-node GPUs per pod (drives the per-role
	// DRA GPU count); replica counts are the xPyD ratio. PP>1 per role is a
	// follow-on (a Deployment can't express multi-node coordination) and is not
	// rendered — the orchestrator/API constrain per-role PP to 1.
	//
	// BothReplicas/BothTP (PRD-63) size the optional co-located "both" pool.
	// BothReplicas == 0 (the default) renders NO both role — the graph is then
	// byte-identical to the two-role prefill/decode graph. A "both"-only run
	// sets PrefillReplicas == DecodeReplicas == 0.
	PrefillReplicas int
	PrefillTP       int
	DecodeReplicas  int
	DecodeTP        int
	BothReplicas    int
	BothTP          int

	// Per-pod CPU/memory requests (GPUs come via the DRA claim).
	CPURequest    string
	MemoryRequest string

	// NetworkMode selects the NIXL transport: "tcp" (UCX_TLS=tcp,... — no EFA)
	// or "efa" (libfabric/RDMA). Follows the same axis as co-located NCCL.
	NetworkMode string

	// NixlModuleDir pins the UCX module set inside the vLLM image (the image
	// ships both cu12 and cu13; the cu13 set is empirically required).
	NixlModuleDir string

	// EPP + sidecar images (llm-d router components). NonCachedTokens gates
	// disaggregation on the uncached prompt-suffix length (EPP decider).
	EPPImage        string
	SidecarImage    string
	NonCachedTokens int
	// EPPZone, when set, pins the (CPU-only) EPP to the same AZ as the serving
	// pods via a topology.kubernetes.io/zone nodeSelector — the EPP is on the
	// request path (ext-proc gRPC per request), so co-locating it with the
	// endpoints it scores avoids a cross-AZ hop. Empty ⇒ no zone constraint.
	EPPZone string

	// PRD-61: run-tunable EPP EndpointPickerConfig knobs. The orchestrator
	// defaults these to the shipped values (2/1/256/31250), so at the defaults
	// the rendered pd-config.yaml is byte-identical to pre-PRD-61.
	//   PrefixCacheScorerWeight / QueueScorerWeight — SHARED across both the
	//     prefill and decode schedulingProfiles (per-profile is a follow-on).
	//   MaxPrefixBlocksToMatch / LRUCapacityPerServer — approx-prefix-cache
	//     producer params (match depth / capacity).
	PrefixCacheScorerWeight int
	QueueScorerWeight       int
	MaxPrefixBlocksToMatch  int
	LRUCapacityPerServer    int

	// DRA GPU device class + scheduling (same as the co-located path).
	GPUDeviceClass      string
	GatewayName         string
	GatewayNamespace    string
	MultiNodeTaintKey   string
	MultiNodeTaintValue string
	DRANodeSelectorKey  string
	DRANodeSelectorVal  string
}

// RenderLLMDDisaggregated renders the PD-disaggregated llm-d object graph as a
// multi-document YAML string (PRD-58). Documents: per-role ResourceClaimTemplates,
// prefill + decode Deployments (decode carries the routing sidecar), per-role
// Services, InferencePool, EPP ConfigMap/SA/RBAC/Deployment/Service, and the
// InferencePool-backed HTTPRoute.
func RenderLLMDDisaggregated(params LLMDDisaggregatedParams) (string, error) {
	return renderTemplate("llmd-disaggregated.yaml.tmpl", params)
}

// RenderCacheJob renders the model cache Job manifest.
func RenderCacheJob(params CacheJobParams) (string, error) {
	return renderTemplate("cache-job.yaml.tmpl", params)
}

// RenderModelDeployment renders the model Deployment + Service manifests.
func RenderModelDeployment(params ModelDeploymentParams) (string, error) {
	return renderTemplate("model-deployment.yaml.tmpl", params)
}

// RenderLoadgenJob renders the load generator Job manifest.
func RenderLoadgenJob(params LoadgenJobParams) (string, error) {
	return renderTemplate("loadgen-job.yaml.tmpl", params)
}

func renderTemplate(name string, data any) (string, error) {
	var buf bytes.Buffer
	if err := templates.ExecuteTemplate(&buf, name, data); err != nil {
		return "", fmt.Errorf("render template %s: %w", name, err)
	}
	return buf.String(), nil
}
