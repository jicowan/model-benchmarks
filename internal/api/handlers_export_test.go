package api

import (
	"strings"
	"testing"

	"github.com/accelbench/accelbench/internal/database"
)

// TestGenerateManifest_EmitsAllVLLMFlags is a regression guard: for a
// fully-populated RunExportDetails, the exported manifest must contain
// every vLLM flag AccelBench passes to the runtime model deployment.
// When a new flag is added to internal/manifest/templates/
// model-deployment.yaml.tmpl, the author must also add the flag to this
// test (and to the export template); otherwise exports drift from
// runtime behavior and the manifest stops reproducing what actually
// ran. See PRD-46 "Export" section for the full flag catalog.
func TestGenerateManifest_EmitsAllVLLMFlags(t *testing.T) {
	q := "int8"
	s3 := "s3://accelbench-models-820537372947/models/meta-llama/Llama-3.1-8B-Instruct"
	mnbt := 8192
	kvDtype := "fp8"
	d := &database.RunExportDetails{
		RunID:                "test-run-id",
		ModelHfID:            "meta-llama/Llama-3.1-8B-Instruct",
		ModelS3URI:           &s3,
		InstanceTypeName:     "g6e.xlarge",
		Framework:            "vllm",
		FrameworkVersion:     "v0.20.1",
		TensorParallelDegree: 1,
		Quantization:         &q,
		MaxModelLen:          8192,
		MaxNumBatchedTokens:  &mnbt,
		KVCacheDtype:         &kvDtype,
		Concurrency:          32,
		AcceleratorType:      "gpu",
		AcceleratorName:      "L40S",
		AcceleratorCount:     1,
		AcceleratorMemoryGiB: 48,
		VCPUs:                4,
		MemoryGiB:            32,
		// PRD-50: GetRunExportDetails resolves UseRunaiStreamer from
		// streamer_mode + model_s3_uri. Tests that hand-build the struct
		// set it explicitly.
		UseRunaiStreamer: true,
	}

	out, err := generateManifest(d)
	if err != nil {
		t.Fatalf("generateManifest: %v", err)
	}

	// Flags every export must emit for a full run config. Extend this
	// slice whenever a new vLLM flag is added to the runtime template.
	want := []string{
		`"--model"`,
		`"--port"`,
		`"8000"`,
		`"--tensor-parallel-size"`,
		`"--trust-remote-code"`,
		`"--max-model-len"`,
		`"8192"`,
		`"--max-num-batched-tokens"`,
		`"--max-num-seqs"`,
		`"32"`,
		`"--kv-cache-dtype"`,
		`"fp8"`,
		`"--load-format"`,
		`"runai_streamer"`,
		`"--model-loader-extra-config"`,
	}
	for _, w := range want {
		if !strings.Contains(out, w) {
			t.Errorf("exported manifest missing %q\n--- rendered manifest ---\n%s", w, out)
		}
	}

	// Headline comment block should surface the full run config so a
	// reader can reproduce the run from the YAML alone.
	wantComments := []string{
		"# Model: meta-llama/Llama-3.1-8B-Instruct",
		"# Weights: " + s3,
		"# Instance: g6e.xlarge",
		"# Tensor Parallel: 1",
		"# Max Model Length: 8192",
		"# Max Num Batched Tokens: 8192",
		"# Max Num Seqs: 32",
		"# KV Cache Dtype: fp8",
	}
	for _, w := range wantComments {
		if !strings.Contains(out, w) {
			t.Errorf("exported manifest comment block missing %q", w)
		}
	}
}

// TestGenerateManifest_HFLoaderPath exercises the branch where the run
// loaded weights from HuggingFace (no S3 URI). --load-format runai_streamer
// must NOT appear; the HF token secret must be referenced.
func TestGenerateManifest_HFLoaderPath(t *testing.T) {
	d := &database.RunExportDetails{
		RunID:                "test-run-id-hf",
		ModelHfID:            "meta-llama/Llama-3.1-8B-Instruct",
		InstanceTypeName:     "g6.xlarge",
		Framework:            "vllm",
		FrameworkVersion:     "v0.20.1",
		TensorParallelDegree: 1,
		MaxModelLen:          4096,
		AcceleratorType:      "gpu",
		AcceleratorName:      "L4",
		AcceleratorCount:     1,
		AcceleratorMemoryGiB: 24,
		VCPUs:                4,
		MemoryGiB:            16,
	}

	out, err := generateManifest(d)
	if err != nil {
		t.Fatalf("generateManifest: %v", err)
	}

	if strings.Contains(out, "runai_streamer") {
		t.Error("HF-loader export should not emit --load-format runai_streamer")
	}
	for _, w := range []string{
		`"--model"`,
		`"meta-llama/Llama-3.1-8B-Instruct"`,
		`"--max-model-len"`,
		`name: hf-token`,
	} {
		if !strings.Contains(out, w) {
			t.Errorf("HF-loader export missing %q", w)
		}
	}
}

// PRD-50: export reproduces the user's streamer knobs — concurrency and
// memory limit must appear verbatim. Previously these were hardcoded.
func TestGenerateManifest_StreamerKnobs(t *testing.T) {
	s3 := "s3://bucket/models/foo"
	conc := 8
	memLimit := 12
	d := &database.RunExportDetails{
		RunID:                  "test-run-streamer",
		ModelHfID:              "mistralai/Mistral-7B-Instruct-v0.3",
		ModelS3URI:             &s3,
		InstanceTypeName:       "g5.4xlarge",
		Framework:              "vllm",
		FrameworkVersion:       "v0.20.1",
		TensorParallelDegree:   1,
		MaxModelLen:            8192,
		AcceleratorType:        "gpu",
		AcceleratorName:        "A10G",
		AcceleratorCount:       1,
		AcceleratorMemoryGiB:   24,
		VCPUs:                  16,
		MemoryGiB:              64,
		StreamerConcurrency:    &conc,
		StreamerMemoryLimitGiB: &memLimit,
		UseRunaiStreamer:       true,
	}
	out, err := generateManifest(d)
	if err != nil {
		t.Fatalf("generateManifest: %v", err)
	}
	// Concurrency must be the user's 8, not the old hardcoded 16.
	if !strings.Contains(out, `"concurrency":8`) {
		t.Errorf("export missing concurrency=8 in model-loader-extra-config:\n%s", out)
	}
	// Memory-limit env var rendered in bytes (12 GiB = 12884901888).
	if !strings.Contains(out, "RUNAI_STREAMER_MEMORY_LIMIT") {
		t.Error("export missing RUNAI_STREAMER_MEMORY_LIMIT env var")
	}
	if !strings.Contains(out, `"12884901888"`) {
		t.Errorf("export missing 12 GiB (12884901888) byte value:\n%s", out)
	}
}

// PRD-50: streamer_mode=off on an S3-backed run must not emit the
// runai_streamer args — vLLM's default loader is used even for S3.
func TestGenerateManifest_StreamerOff(t *testing.T) {
	s3 := "s3://bucket/models/foo"
	d := &database.RunExportDetails{
		RunID:                "test-run-streamer-off",
		ModelHfID:            "mistralai/Mistral-7B-Instruct-v0.3",
		ModelS3URI:           &s3,
		InstanceTypeName:     "g5.4xlarge",
		Framework:            "vllm",
		FrameworkVersion:     "v0.20.1",
		TensorParallelDegree: 1,
		MaxModelLen:          8192,
		AcceleratorType:      "gpu",
		AcceleratorName:      "A10G",
		AcceleratorCount:     1,
		AcceleratorMemoryGiB: 24,
		VCPUs:                16,
		MemoryGiB:            64,
		UseRunaiStreamer:     false, // streamer_mode=off
	}
	out, err := generateManifest(d)
	if err != nil {
		t.Fatalf("generateManifest: %v", err)
	}
	if strings.Contains(out, "runai_streamer") {
		t.Errorf("streamer-off export should not emit --load-format runai_streamer:\n%s", out)
	}
	if strings.Contains(out, "RUNAI_STREAMER_MEMORY_LIMIT") {
		t.Error("streamer-off export should not set RUNAI_STREAMER_MEMORY_LIMIT")
	}
	// The S3 URI should still be passed as the model path.
	if !strings.Contains(out, s3) {
		t.Errorf("streamer-off export missing model S3 URI:\n%s", out)
	}
}
