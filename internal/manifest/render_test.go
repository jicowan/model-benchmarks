package manifest

import (
	"strings"
	"testing"
)

func TestRenderModelDeployment_GPU(t *testing.T) {
	params := ModelDeploymentParams{
		Name:                 "bench-test123",
		Namespace:            "accelbench",
		ModelHfID:            "meta-llama/Llama-3.1-70B-Instruct",
		HfToken:              "hf_test_token",
		Framework:            "vllm",
		FrameworkVersion:     "v0.6.0",
		TensorParallelDegree: 8,
		Quantization:         "fp16",
		AcceleratorType:      "gpu",
		AcceleratorCount:     8,
		AcceleratorMemoryGiB: 640,
		InstanceTypeName:     "p5.48xlarge",
		InstanceFamily:       "p5",
		CPURequest:           "8",
		MemoryRequest:        "32Gi",
	}

	out, err := RenderModelDeployment(params)
	if err != nil {
		t.Fatalf("RenderModelDeployment: %v", err)
	}

	checks := []struct {
		name string
		want string
	}{
		{"deployment name", "name: bench-test123"},
		{"namespace", "namespace: accelbench"},
		{"model arg", "meta-llama/Llama-3.1-70B-Instruct"},
		{"vllm image", "vllm/vllm-openai:v0.6.0"},
		{"gpu toleration", "nvidia.com/gpu"},
		{"gpu resource request", `nvidia.com/gpu: "8"`},
		{"tensor parallel", `"8"`},
		{"quantization dtype", `"float16"`},
		{"node selector instance type", "node.kubernetes.io/instance-type: p5.48xlarge"},
		{"hf token", "hf_test_token"},
		{"Service kind", "kind: Service"},
		{"service port 8000", "port: 8000"},
		{"readiness probe", "/health"},
	}

	for _, c := range checks {
		if !strings.Contains(out, c.want) {
			t.Errorf("%s: output does not contain %q", c.name, c.want)
		}
	}

	// Should NOT contain Neuron-specific content.
	if strings.Contains(out, "aws.amazon.com/neuron") {
		t.Error("GPU deployment should not contain neuron resource")
	}
	if strings.Contains(out, "neuron-monitor") {
		t.Error("GPU deployment should not contain neuron-monitor sidecar")
	}
}

func TestRenderModelDeployment_Neuron(t *testing.T) {
	params := ModelDeploymentParams{
		Name:                 "bench-neuron",
		Namespace:            "accelbench",
		ModelHfID:            "meta-llama/Llama-3.1-8B-Instruct",
		Framework:            "vllm-neuron",
		FrameworkVersion:     "v0.6.0",
		TensorParallelDegree: 2,
		AcceleratorType:      "neuron",
		AcceleratorCount:     2,
		AcceleratorMemoryGiB: 32,
		InstanceTypeName:     "inf2.xlarge",
		InstanceFamily:       "inf2",
		CPURequest:           "4",
		MemoryRequest:        "16Gi",
	}

	out, err := RenderModelDeployment(params)
	if err != nil {
		t.Fatalf("RenderModelDeployment: %v", err)
	}

	checks := []struct {
		name string
		want string
	}{
		{"neuron image", "vllm/vllm-neuron:v0.6.0"},
		{"neuron toleration", "aws.amazon.com/neuron"},
		{"neuron resource", `aws.amazon.com/neuron: "2"`},
		{"instance type", "node.kubernetes.io/instance-type: inf2.xlarge"},
	}

	for _, c := range checks {
		if !strings.Contains(out, c.want) {
			t.Errorf("%s: output does not contain %q", c.name, c.want)
		}
	}

	// Should NOT contain GPU-specific content.
	if strings.Contains(out, "nvidia.com/gpu") {
		t.Error("Neuron deployment should not contain nvidia.com/gpu")
	}
	if strings.Contains(out, "dcgm-exporter") {
		t.Error("Neuron deployment should not contain dcgm-exporter")
	}
}

func TestRenderModelDeployment_NoQuantization(t *testing.T) {
	params := ModelDeploymentParams{
		Name:                 "bench-noq",
		Namespace:            "accelbench",
		ModelHfID:            "mistralai/Mistral-7B-v0.1",
		Framework:            "vllm",
		FrameworkVersion:     "v0.6.0",
		TensorParallelDegree: 1,
		Quantization:         "",
		AcceleratorType:      "gpu",
		AcceleratorCount:     1,
		InstanceTypeName:     "g5.xlarge",
		InstanceFamily:       "g5",
		CPURequest:           "4",
		MemoryRequest:        "16Gi",
	}

	out, err := RenderModelDeployment(params)
	if err != nil {
		t.Fatalf("RenderModelDeployment: %v", err)
	}

	if strings.Contains(out, "--dtype") {
		t.Error("output should not contain --dtype when quantization is empty")
	}
	if strings.Contains(out, "--quantization") {
		t.Error("output should not contain --quantization when quantization is empty")
	}
}

func TestRenderLoadgenJob(t *testing.T) {
	params := LoadgenJobParams{
		Name:                 "loadgen-abc123",
		Namespace:            "accelbench",
		TargetHost:           "bench-test123",
		TargetPort:           8000,
		ModelHfID:            "meta-llama/Llama-3.1-70B-Instruct",
		Concurrency:          16,
		InputSequenceLength:  512,
		OutputSequenceLength: 256,
		DatasetName:          "sharegpt",
		NumRequests:          200,
		WarmupRequests:       10,
	}

	out, err := RenderLoadgenJob(params)
	if err != nil {
		t.Fatalf("RenderLoadgenJob: %v", err)
	}

	checks := []struct {
		name string
		want string
	}{
		{"job name", "name: loadgen-abc123"},
		{"namespace", "namespace: accelbench"},
		{"target url", "http://bench-test123:8000/v1/completions"},
		{"model id env", "meta-llama/Llama-3.1-70B-Instruct"},
		{"concurrency env", `value: "16"`},
		{"input seq len", `value: "512"`},
		{"output seq len", `value: "256"`},
		{"dataset env", `value: "sharegpt"`},
		{"num requests", `value: "200"`},
		{"warmup requests", `value: "10"`},
		{"system node affinity", "accelbench/node-type"},
		{"json output", `value: "json"`},
		{"min duration env", "MIN_DURATION_SECONDS"},
		{"backoff limit", "backoffLimit: 0"},
	}

	for _, c := range checks {
		if !strings.Contains(out, c.want) {
			t.Errorf("%s: output does not contain %q", c.name, c.want)
		}
	}
}

func TestRenderModelDeployment_MultiDocument(t *testing.T) {
	params := ModelDeploymentParams{
		Name:                 "bench-multi",
		Namespace:            "accelbench",
		ModelHfID:            "test/model",
		Framework:            "vllm",
		FrameworkVersion:     "v0.6.0",
		TensorParallelDegree: 1,
		AcceleratorType:      "gpu",
		AcceleratorCount:     1,
		InstanceTypeName:     "g5.xlarge",
		InstanceFamily:       "g5",
		CPURequest:           "4",
		MemoryRequest:        "16Gi",
	}

	out, err := RenderModelDeployment(params)
	if err != nil {
		t.Fatalf("RenderModelDeployment: %v", err)
	}

	// Should contain both Deployment and Service separated by ---
	if !strings.Contains(out, "kind: Deployment") {
		t.Error("output missing Deployment")
	}
	if !strings.Contains(out, "kind: Service") {
		t.Error("output missing Service")
	}
	if !strings.Contains(out, "---") {
		t.Error("output missing YAML document separator")
	}
}
