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
	CPURequest           string
	MemoryRequest        string
	ModelS3URI           string // s3://bucket/models/org/model (empty = use HF)
	UseRunaiStreamer      bool   // true = --load-format runai_streamer
	ModelServiceAccount   string // K8s service account for S3 access
	StreamerConcurrency   int    // runai_streamer concurrency (default 16)
}

// LoadgenJobParams holds values for rendering the load generator Job.
type LoadgenJobParams struct {
	Name               string
	Namespace          string
	InferencePerfImage string // inference-perf container image
	ConfigMapName      string // ConfigMap containing inference-perf config
	ResultsS3Bucket    string // S3 bucket for results upload
	ResultsS3Key       string // S3 key for results file
	AWSRegion          string // AWS region for S3 upload
	HfToken            string // HuggingFace token for downloading datasets (sharegpt, cnn_dailymail)
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
