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
}

// LoadgenJobParams holds values for rendering the load generator Job.
type LoadgenJobParams struct {
	Name                 string
	Namespace            string
	LoadgenImage         string // full image URI
	TargetHost           string // model service name
	TargetPort           int
	ModelHfID            string
	Concurrency          int
	InputSequenceLength  int
	OutputSequenceLength int
	DatasetName          string
	NumRequests          int
	WarmupRequests       int
	MinDurationSeconds   int
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
