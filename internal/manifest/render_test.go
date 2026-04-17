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
		{"neuron image", "public.ecr.aws/neuron/pytorch-inference-vllm-neuronx:0.13.0-neuronx-py312-sdk2.28.0-ubuntu24.04"},
		{"neuron toleration", "aws.amazon.com/neuron"},
		{"neuron resource", `aws.amazon.com/neuron: "1"`}, // 2 NeuronCores / 2 = 1 Neuron device
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
		Name:               "loadgen-abc123",
		Namespace:          "accelbench",
		InferencePerfImage: "quay.io/inference-perf/inference-perf:v0.2.0",
		ConfigMapName:      "loadgen-config-abc123",
		ResultsS3Bucket:    "accelbench-results",
		ResultsS3Key:       "results/test-run.json",
		AWSRegion:          "us-east-2",
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
		{"inference-perf image", "quay.io/inference-perf/inference-perf:v0.2.0"},
		{"config file arg", "--config_file"},
		{"config mount path", "/workspace/config.yml"},
		{"configmap name", "loadgen-config-abc123"},
		{"results mount", "/tmp/results"},
		{"s3 bucket", "accelbench-results"},
		{"s3 key", "results/test-run.json"},
		{"aws region", "us-east-2"},
		{"system node affinity", "accelbench/node-type"},
		{"service account", "serviceAccountName: accelbench-loadgen"},
		{"backoff limit", "backoffLimit: 0"},
		{"s3-upload sidecar", "name: s3-upload"},
	}

	for _, c := range checks {
		if !strings.Contains(out, c.want) {
			t.Errorf("%s: output does not contain %q", c.name, c.want)
		}
	}
}

func TestRenderLoadgenJob_NoS3(t *testing.T) {
	params := LoadgenJobParams{
		Name:               "loadgen-nos3",
		Namespace:          "accelbench",
		InferencePerfImage: "quay.io/inference-perf/inference-perf:v0.2.0",
		ConfigMapName:      "loadgen-config-nos3",
		ResultsS3Bucket:    "", // No S3
	}

	out, err := RenderLoadgenJob(params)
	if err != nil {
		t.Fatalf("RenderLoadgenJob: %v", err)
	}

	// Should NOT contain S3 sidecar when no bucket specified
	if strings.Contains(out, "s3-upload") {
		t.Error("output should not contain s3-upload sidecar when no S3 bucket")
	}

	// Should still contain main inference-perf container
	if !strings.Contains(out, "inference-perf") {
		t.Error("output missing inference-perf container")
	}
}

func TestRenderInferencePerfConfig(t *testing.T) {
	params := InferencePerfConfigParams{
		ModelHfID:    "meta-llama/Llama-3.1-8B-Instruct",
		TargetHost:   "bench-test123",
		TargetPort:   8000,
		Streaming:    true,
		DatasetType:  "synthetic",
		InputMean:    256,
		InputStdDev:  64,
		InputMin:     128,
		InputMax:     512,
		OutputMean:   128,
		OutputStdDev: 32,
		OutputMin:    64,
		OutputMax:    256,
		LoadType:     "constant",
		Stages:       []LoadStage{{Rate: 5, Duration: 120}},
		NumWorkers:   4,
	}

	out, err := RenderInferencePerfConfig(params)
	if err != nil {
		t.Fatalf("RenderInferencePerfConfig: %v", err)
	}

	checks := []struct {
		name string
		want string
	}{
		{"server type", "type: vllm"},
		{"model name", "model_name: meta-llama/Llama-3.1-8B-Instruct"},
		{"base url", "base_url: http://bench-test123:8000"},
		{"ignore_eos", "ignore_eos: true"},
		{"tokenizer", "pretrained_model_name_or_path: meta-llama/Llama-3.1-8B-Instruct"},
		{"api type", "type: completion"},
		{"streaming", "streaming: true"},
		{"data type", "type: synthetic"},
		{"input mean", "mean: 256"},
		{"output mean", "mean: 128"},
		{"load type", "type: constant"},
		{"rate", "rate: 5"},
		{"duration", "duration: 120"},
		{"workers", "num_workers: 4"},
		{"storage path", "path: /tmp/results"},
	}

	for _, c := range checks {
		if !strings.Contains(out, c.want) {
			t.Errorf("%s: output does not contain %q", c.name, c.want)
		}
	}
}

func TestRenderInferencePerfConfig_MultiStage(t *testing.T) {
	params := InferencePerfConfigParams{
		ModelHfID:    "test/model",
		TargetHost:   "bench-test",
		TargetPort:   8000,
		Streaming:    true,
		DatasetType:  "synthetic",
		InputMean:    256,
		InputStdDev:  64,
		InputMin:     128,
		InputMax:     512,
		OutputMean:   128,
		OutputStdDev: 32,
		OutputMin:    64,
		OutputMax:    256,
		LoadType:     "constant",
		Stages: []LoadStage{
			{Rate: 2, Duration: 60},
			{Rate: 5, Duration: 60},
			{Rate: 10, Duration: 60},
			{Rate: 20, Duration: 60},
		},
		NumWorkers: 8,
	}

	out, err := RenderInferencePerfConfig(params)
	if err != nil {
		t.Fatalf("RenderInferencePerfConfig: %v", err)
	}

	// Verify all stages are present
	checks := []string{"rate: 2", "rate: 5", "rate: 10", "rate: 20"}
	for _, want := range checks {
		if !strings.Contains(out, want) {
			t.Errorf("output does not contain %q", want)
		}
	}
}

func TestRenderModelDeployment_S3Runai(t *testing.T) {
	params := ModelDeploymentParams{
		Name:                 "bench-s3",
		Namespace:            "accelbench",
		ModelHfID:            "meta-llama/Llama-3.1-8B-Instruct",
		Framework:            "vllm",
		FrameworkVersion:     "v0.8.5",
		TensorParallelDegree: 1,
		AcceleratorType:      "gpu",
		AcceleratorCount:     1,
		InstanceTypeName:     "g6e.xlarge",
		InstanceFamily:       "g6e",
		CPURequest:           "4",
		MemoryRequest:        "16Gi",
		ModelS3URI:           "s3://accelbench-models-123/models/meta-llama/Llama-3.1-8B-Instruct",
		UseRunaiStreamer:     true,
		ModelServiceAccount:  "accelbench-model",
		StreamerConcurrency:  16,
	}

	out, err := RenderModelDeployment(params)
	if err != nil {
		t.Fatalf("RenderModelDeployment: %v", err)
	}

	checks := []struct {
		name string
		want string
	}{
		{"s3 model uri", "s3://accelbench-models-123/models/meta-llama/Llama-3.1-8B-Instruct"},
		{"runai streamer", "runai_streamer"},
		{"concurrency config", `"concurrency":16`},
		{"standard vllm image", "vllm/vllm-openai:v0.8.5"},
		{"service account", "serviceAccountName: accelbench-model"},
	}
	for _, c := range checks {
		if !strings.Contains(out, c.want) {
			t.Errorf("%s: output does not contain %q", c.name, c.want)
		}
	}

}

func TestRenderModelDeployment_S3NoBitsandbytes(t *testing.T) {
	params := ModelDeploymentParams{
		Name:                 "bench-s3-quant",
		Namespace:            "accelbench",
		ModelHfID:            "meta-llama/Llama-3.1-70B-Instruct",
		Framework:            "vllm",
		FrameworkVersion:     "v0.8.5",
		TensorParallelDegree: 4,
		Quantization:         "int8",
		AcceleratorType:      "gpu",
		AcceleratorCount:     4,
		InstanceTypeName:     "g5.12xlarge",
		InstanceFamily:       "g5",
		CPURequest:           "8",
		MemoryRequest:        "32Gi",
		ModelS3URI:           "s3://bucket/models/llama-70b",
		UseRunaiStreamer:     true,
		ModelServiceAccount:  "accelbench-model",
		StreamerConcurrency:  16,
	}

	out, err := RenderModelDeployment(params)
	if err != nil {
		t.Fatalf("RenderModelDeployment: %v", err)
	}

	// Should use runai_streamer, not bitsandbytes
	if !strings.Contains(out, "runai_streamer") {
		t.Error("expected runai_streamer in output")
	}
	if strings.Contains(out, "bitsandbytes") {
		t.Error("bitsandbytes should be omitted when UseRunaiStreamer is true")
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
