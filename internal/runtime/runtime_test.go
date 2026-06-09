package runtime

import (
	"os"
	"slices"
	"strings"
	"testing"
)

// --- Registry tests ---

func TestGet_ValidFrameworks(t *testing.T) {
	for _, name := range []string{"vllm", "vllm-neuron", "sglang"} {
		rt, err := Get(name)
		if err != nil {
			t.Errorf("Get(%q): unexpected error: %v", name, err)
		}
		if rt.Name() != name {
			t.Errorf("Get(%q).Name() = %q", name, rt.Name())
		}
	}
}

func TestGet_Invalid(t *testing.T) {
	_, err := Get("tensorrt-llm")
	if err == nil {
		t.Error("Get(tensorrt-llm) should fail")
	}
}

func TestNames(t *testing.T) {
	names := Names()
	if len(names) < 3 {
		t.Fatalf("expected at least 3 runtimes, got %v", names)
	}
	for i := 1; i < len(names); i++ {
		if names[i] < names[i-1] {
			t.Errorf("Names() not sorted: %v", names)
		}
	}
}

func TestForAccelerator(t *testing.T) {
	if rt := ForAccelerator("gpu"); rt.Name() != "vllm" {
		t.Errorf("ForAccelerator(gpu) = %q, want vllm", rt.Name())
	}
	if rt := ForAccelerator("neuron"); rt.Name() != "vllm-neuron" {
		t.Errorf("ForAccelerator(neuron) = %q, want vllm-neuron", rt.Name())
	}
}

func TestSupportsAccelerator(t *testing.T) {
	vllm, _ := Get("vllm")
	if !SupportsAccelerator(vllm, "gpu") {
		t.Error("vllm should support gpu")
	}
	if SupportsAccelerator(vllm, "neuron") {
		t.Error("vllm should not support neuron")
	}

	neuron, _ := Get("vllm-neuron")
	if !SupportsAccelerator(neuron, "neuron") {
		t.Error("vllm-neuron should support neuron")
	}
	if SupportsAccelerator(neuron, "gpu") {
		t.Error("vllm-neuron should not support gpu")
	}
}

// --- VLLMgpu tests ---

func TestVLLMgpu_ContainerName(t *testing.T) {
	rt := &VLLMgpu{}
	if rt.ContainerName() != "vllm" {
		t.Errorf("ContainerName() = %q", rt.ContainerName())
	}
}

func TestVLLMgpu_DefaultImage(t *testing.T) {
	rt := &VLLMgpu{}
	if got := rt.DefaultImage("v0.6.0", ""); got != "vllm/vllm-openai:v0.6.0" {
		t.Errorf("DefaultImage without pull-through = %q", got)
	}
	if got := rt.DefaultImage("v0.6.0", "123456789012.dkr.ecr.us-east-2.amazonaws.com"); got != "123456789012.dkr.ecr.us-east-2.amazonaws.com/dockerhub/vllm/vllm-openai:v0.6.0" {
		t.Errorf("DefaultImage with pull-through = %q", got)
	}
}

func TestVLLMgpu_ResolveVersion(t *testing.T) {
	rt := &VLLMgpu{}
	tv := ToolVersions{FrameworkVersion: "v0.8.5", SGLangVersion: "v0.4.10"}
	if got := rt.ResolveVersion(tv); got != "v0.8.5" {
		t.Errorf("ResolveVersion = %q, want v0.8.5", got)
	}
}

func TestVLLMgpu_ResolveImageOverride(t *testing.T) {
	rt := &VLLMgpu{}
	os.Setenv("VLLM_IMAGE", "custom/vllm:test")
	defer os.Unsetenv("VLLM_IMAGE")
	if got := rt.ResolveImageOverride(); got != "custom/vllm:test" {
		t.Errorf("ResolveImageOverride = %q", got)
	}
}

func TestVLLMgpu_BuildArgs_Basic(t *testing.T) {
	rt := &VLLMgpu{}
	cmd, args := rt.BuildArgs(ContainerParams{
		ModelHfID:            "meta-llama/Llama-3.1-70B-Instruct",
		TensorParallelDegree: 8,
		Quantization:         "fp16",
	})
	if cmd != nil {
		t.Errorf("command should be nil, got %v", cmd)
	}
	assertContains(t, args, "--model", "meta-llama/Llama-3.1-70B-Instruct")
	assertContains(t, args, "--port", "8000")
	assertContains(t, args, "--tensor-parallel-size", "8")
	assertContains(t, args, "--trust-remote-code")
	assertContains(t, args, "--dtype", "float16")
}

func TestVLLMgpu_BuildArgs_RunaiStreamer(t *testing.T) {
	rt := &VLLMgpu{}
	_, args := rt.BuildArgs(ContainerParams{
		ModelHfID:            "meta-llama/Llama-3.1-8B-Instruct",
		ModelS3URI:           "s3://bucket/models/llama",
		UseRunaiStreamer:     true,
		TensorParallelDegree: 1,
		StreamerConcurrency:  8,
		Quantization:         "int8",
	})
	assertContains(t, args, "--model", "s3://bucket/models/llama")
	assertContains(t, args, "--load-format", "runai_streamer")
	assertContains(t, args, `{"concurrency":8}`)
	// quantization should be suppressed when streaming
	assertNotContains(t, args, "--quantization")
	assertNotContains(t, args, "bitsandbytes")
}

func TestVLLMgpu_BuildArgs_AllKnobs(t *testing.T) {
	rt := &VLLMgpu{}
	_, args := rt.BuildArgs(ContainerParams{
		ModelHfID:            "test/model",
		TensorParallelDegree: 4,
		MaxModelLen:          4096,
		MaxNumBatchedTokens:  8192,
		MaxNumSeqs:           128,
		KVCacheDtype:         "fp8",
	})
	assertContains(t, args, "--max-model-len", "4096")
	assertContains(t, args, "--max-num-batched-tokens", "8192")
	assertContains(t, args, "--max-num-seqs", "128")
	assertContains(t, args, "--kv-cache-dtype", "fp8")
}

func TestVLLMgpu_MapQuantization(t *testing.T) {
	rt := &VLLMgpu{}
	tests := []struct {
		quant    string
		streamer bool
		want     []string
	}{
		{"fp16", false, []string{"--dtype", "float16"}},
		{"int8", false, []string{"--quantization", "bitsandbytes", "--load-format", "bitsandbytes"}},
		{"int4", false, []string{"--quantization", "bitsandbytes", "--load-format", "bitsandbytes"}},
		{"", false, nil},
		{"fp16", true, nil},
	}
	for _, tt := range tests {
		got := rt.MapQuantization(tt.quant, tt.streamer)
		if !slices.Equal(got, tt.want) {
			t.Errorf("MapQuantization(%q, %v) = %v, want %v", tt.quant, tt.streamer, got, tt.want)
		}
	}
}

// --- VLLMneuron tests ---

func TestVLLMneuron_DefaultImage(t *testing.T) {
	rt := &VLLMneuron{}
	got := rt.DefaultImage("v0.6.0", "anything")
	if got != neuronImage {
		t.Errorf("DefaultImage = %q, want hardcoded neuron image", got)
	}
}

func TestVLLMneuron_BuildArgs(t *testing.T) {
	rt := &VLLMneuron{}
	cmd, args := rt.BuildArgs(ContainerParams{
		ModelHfID:            "meta-llama/Llama-3.1-8B-Instruct",
		TensorParallelDegree: 2,
		MaxModelLen:          2048,
	})
	if len(cmd) != 1 || cmd[0] != "vllm" {
		t.Errorf("command = %v, want [vllm]", cmd)
	}
	assertContains(t, args, "serve", "meta-llama/Llama-3.1-8B-Instruct")
	assertContains(t, args, "--block-size", "32")
	assertContains(t, args, "--tensor-parallel-size", "2")
	assertContains(t, args, "--max-model-len", "2048")
}

func TestVLLMneuron_NoQuantization(t *testing.T) {
	rt := &VLLMneuron{}
	got := rt.MapQuantization("fp8", false)
	if got != nil {
		t.Errorf("MapQuantization should return nil for neuron, got %v", got)
	}
}

// --- SGLang tests ---

func TestSGLang_ContainerName(t *testing.T) {
	rt := &SGLang{}
	if rt.ContainerName() != "sglang" {
		t.Errorf("ContainerName() = %q", rt.ContainerName())
	}
}

func TestSGLang_DefaultImage(t *testing.T) {
	rt := &SGLang{}
	if got := rt.DefaultImage("v0.4.10.post2-cu126", ""); got != "lmsysorg/sglang:v0.4.10.post2-cu126" {
		t.Errorf("DefaultImage without pull-through = %q", got)
	}
	if got := rt.DefaultImage("v0.5.2-cu126", "123456789012.dkr.ecr.us-east-2.amazonaws.com"); got != "123456789012.dkr.ecr.us-east-2.amazonaws.com/dockerhub/lmsysorg/sglang:v0.5.2-cu126" {
		t.Errorf("DefaultImage with pull-through = %q", got)
	}
}

func TestSGLang_ResolveVersion(t *testing.T) {
	rt := &SGLang{}
	tv := ToolVersions{FrameworkVersion: "v0.8.5", SGLangVersion: "v0.4.10.post2-cu126"}
	if got := rt.ResolveVersion(tv); got != "v0.4.10.post2-cu126" {
		t.Errorf("ResolveVersion = %q", got)
	}
}

func TestSGLang_ResolveImageOverride(t *testing.T) {
	rt := &SGLang{}
	os.Setenv("SGLANG_IMAGE", "custom/sglang:test")
	defer os.Unsetenv("SGLANG_IMAGE")
	if got := rt.ResolveImageOverride(); got != "custom/sglang:test" {
		t.Errorf("ResolveImageOverride = %q", got)
	}
}

func TestSGLang_BuildArgs_Basic(t *testing.T) {
	rt := &SGLang{}
	cmd, args := rt.BuildArgs(ContainerParams{
		ModelHfID:            "meta-llama/Llama-3.1-8B-Instruct",
		TensorParallelDegree: 2,
		Quantization:         "fp8",
		MaxModelLen:          8192,
		MaxNumSeqs:           64,
	})
	if len(cmd) != 1 || cmd[0] != "python3" {
		t.Errorf("command = %v, want [python3]", cmd)
	}
	assertContains(t, args, "-m", "sglang.launch_server")
	assertContains(t, args, "--model-path", "meta-llama/Llama-3.1-8B-Instruct")
	assertContains(t, args, "--host", "0.0.0.0")
	assertContains(t, args, "--port", "8000")
	assertContains(t, args, "--tp-size", "2")
	assertContains(t, args, "--trust-remote-code")
	assertContains(t, args, "--quantization", "fp8")
	assertContains(t, args, "--context-length", "8192")
	assertContains(t, args, "--max-running-requests", "64")

	// Must NOT have vLLM-style flags
	assertNotContains(t, args, "--tensor-parallel-size")
	assertNotContains(t, args, "--max-model-len")
	assertNotContains(t, args, "--max-num-seqs")
}

func TestSGLang_BuildArgs_S3Model_FallsBackToHF(t *testing.T) {
	rt := &SGLang{}
	_, args := rt.BuildArgs(ContainerParams{
		ModelHfID:            "meta-llama/Llama-3.1-8B-Instruct",
		ModelS3URI:           "s3://bucket/models/llama",
		UseRunaiStreamer:     true,
		TensorParallelDegree: 2,
	})
	// SGLang doesn't support S3 loading in upstream image (no boto3),
	// so it always uses the HF path.
	assertContains(t, args, "--model-path", "meta-llama/Llama-3.1-8B-Instruct")
	assertNotContains(t, args, "s3://")
	assertNotContains(t, args, "runai_streamer")
	assertNotContains(t, args, "remote")
}

func TestSGLang_BuildArgs_EnableMetrics(t *testing.T) {
	rt := &SGLang{}
	_, args := rt.BuildArgs(ContainerParams{
		ModelHfID:            "test/model",
		TensorParallelDegree: 1,
	})
	assertContains(t, args, "--enable-metrics")
	// --enable-mixed-chunk is on by default to keep TTFT tail latency low
	// under continuous streaming load.
	assertContains(t, args, "--enable-mixed-chunk")
}

func TestSGLang_BuildArgs_AttentionBackend_NonHopper(t *testing.T) {
	rt := &SGLang{}
	// L4 (Ada) and A10G (Ampere) are non-Hopper → force triton.
	for _, gpu := range []string{"L4", "A10G", "L40S", "A100"} {
		_, args := rt.BuildArgs(ContainerParams{
			ModelHfID:            "test/model",
			TensorParallelDegree: 1,
			AcceleratorName:      gpu,
		})
		assertContains(t, args, "--attention-backend", "triton")
	}
}

func TestSGLang_BuildArgs_AttentionBackend_Hopper(t *testing.T) {
	rt := &SGLang{}
	// Hopper (H100/H200) and unknown/empty keep SGLang's default backend.
	for _, gpu := range []string{"H100", "H200", ""} {
		_, args := rt.BuildArgs(ContainerParams{
			ModelHfID:            "test/model",
			TensorParallelDegree: 1,
			AcceleratorName:      gpu,
		})
		assertNotContains(t, args, "--attention-backend")
	}
}

func TestSGLang_BuildArgs_SchedulerKnobs(t *testing.T) {
	rt := &SGLang{}
	_, args := rt.BuildArgs(ContainerParams{
		ModelHfID:            "test/model",
		TensorParallelDegree: 1,
		ChunkedPrefillSize:   4096,
		MemFractionStatic:    0.85,
	})
	assertContains(t, args, "--chunked-prefill-size", "4096")
	assertContains(t, args, "--mem-fraction-static", "0.85")
}

func TestSGLang_BuildArgs_SchedulerKnobs_ZeroOmitted(t *testing.T) {
	rt := &SGLang{}
	_, args := rt.BuildArgs(ContainerParams{
		ModelHfID:            "test/model",
		TensorParallelDegree: 1,
	})
	assertNotContains(t, args, "--chunked-prefill-size")
	assertNotContains(t, args, "--mem-fraction-static")
}

func TestSGLang_MapQuantization(t *testing.T) {
	rt := &SGLang{}
	tests := []struct {
		quant string
		want  []string
	}{
		{"fp8", []string{"--quantization", "fp8"}},
		{"int8", []string{"--quantization", "w8a8_int8"}},
		{"int4", []string{"--quantization", "awq"}},
		{"", nil},
	}
	for _, tt := range tests {
		got := rt.MapQuantization(tt.quant, false)
		if !slices.Equal(got, tt.want) {
			t.Errorf("MapQuantization(%q) = %v, want %v", tt.quant, got, tt.want)
		}
	}
}

// --- helpers ---

func assertContains(t *testing.T, args []string, seq ...string) {
	t.Helper()
	joined := strings.Join(args, "\x00")
	target := strings.Join(seq, "\x00")
	if !strings.Contains(joined, target) {
		t.Errorf("args %v does not contain sequence %v", args, seq)
	}
}

func assertNotContains(t *testing.T, args []string, s string) {
	t.Helper()
	for _, a := range args {
		if a == s {
			t.Errorf("args should not contain %q but does: %v", s, args)
			return
		}
	}
}
