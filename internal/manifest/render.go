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

	// Container image + command. Resolved by the caller via the llm-d
	// Runtime (image override → default GHCR image; args from BuildArgs).
	Image            string
	Command          []string // nil = use image entrypoint
	Args             []string
	ContainerName    string // k8s container name (e.g. "vllm")
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
}

// RenderLLMDDeployment renders the multi-node llm-d object graph as a
// multi-document YAML string (PRD-56). Documents: ResourceClaimTemplate(s),
// LeaderWorkerSet, Service, InferencePool, HTTPRoute.
func RenderLLMDDeployment(params LLMDDeploymentParams) (string, error) {
	return renderTemplate("llmd-deployment.yaml.tmpl", params)
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
