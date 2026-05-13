package manifest

import (
	"bytes"
	"embed"
	"fmt"
	"text/template"
)

//go:embed templates/*.yaml.tmpl
var templateFS embed.FS

var templates *template.Template

func init() {
	var err error
	templates, err = template.New("").Funcs(template.FuncMap{
		"sub": func(a, b int) int { return a - b },
		"div": func(a, b int) int { return a / b },
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
	Framework            string // "vllm" or "vllm-neuron"
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

// CacheJobParams holds values for rendering the model cache Job.
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
