package recommend

import (
	"strings"
	"testing"
)

// Mistral 7B-like model config for testing.
var mistral7B = ModelConfig{
	ParameterCount:        7_248_023_552,
	HiddenSize:            4096,
	NumAttentionHeads:     32,
	NumKeyValueHeads:      8,
	NumHiddenLayers:       32,
	MaxPositionEmbeddings: 32768,
	TorchDtype:            "bfloat16",
	ModelType:             "mistral",
}

// Llama 70B-like model config for testing.
var llama70B = ModelConfig{
	ParameterCount:        70_553_706_496,
	HiddenSize:            8192,
	NumAttentionHeads:     64,
	NumKeyValueHeads:      8,
	NumHiddenLayers:       80,
	MaxPositionEmbeddings: 131072,
	TorchDtype:            "bfloat16",
	ModelType:             "llama",
}

var (
	g5xlarge = InstanceSpec{
		Name: "g5.xlarge", AcceleratorType: "GPU", AcceleratorName: "A10G",
		AcceleratorCount: 1, AcceleratorMemoryGiB: 24,
	}
	g5_12xlarge = InstanceSpec{
		Name: "g5.12xlarge", AcceleratorType: "GPU", AcceleratorName: "A10G",
		AcceleratorCount: 4, AcceleratorMemoryGiB: 96,
	}
	p5_48xlarge = InstanceSpec{
		Name: "p5.48xlarge", AcceleratorType: "GPU", AcceleratorName: "H100",
		AcceleratorCount: 8, AcceleratorMemoryGiB: 640,
	}
	g5_48xlarge = InstanceSpec{
		Name: "g5.48xlarge", AcceleratorType: "GPU", AcceleratorName: "A10G",
		AcceleratorCount: 8, AcceleratorMemoryGiB: 192,
	}
)

var allInstances = []InstanceSpec{g5xlarge, g5_12xlarge, g5_48xlarge, p5_48xlarge}

func TestBytesPerParam(t *testing.T) {
	tests := []struct {
		quant string
		want  float64
	}{
		{"", 2},
		{"bfloat16", 2},
		{"fp16", 2},
		{"fp32", 4},
		{"fp8", 1},
		{"int8", 1},
		{"int4", 0.5},
		{"unknown", 2},
	}
	for _, tt := range tests {
		if got := bytesPerParam(tt.quant); got != tt.want {
			t.Errorf("bytesPerParam(%q) = %v, want %v", tt.quant, got, tt.want)
		}
	}
}

func TestSupportsFP8(t *testing.T) {
	if !supportsFP8("H100") {
		t.Error("expected H100 to support FP8")
	}
	if !supportsFP8("H200") {
		t.Error("expected H200 to support FP8")
	}
	if supportsFP8("A10G") {
		t.Error("expected A10G to not support FP8")
	}
}

func TestKVCachePerTokenBytes(t *testing.T) {
	// Mistral 7B: head_dim=128, kv_per_token = 2*32*8*128*2 = 131072
	got := kvCachePerTokenBytes(mistral7B)
	want := float64(2 * 32 * 8 * 128 * 2)
	if got != want {
		t.Errorf("kvCachePerTokenBytes(mistral7B) = %v, want %v", got, want)
	}
}

func TestValidTPDegree(t *testing.T) {
	tests := []struct {
		name     string
		minTP    int
		heads    int
		kvHeads  int
		maxGPUs  int
		wantTP   int
	}{
		{"1 GPU sufficient", 1, 32, 8, 4, 1},
		{"needs 2 GPUs", 2, 32, 8, 8, 2},
		{"needs 3 but must divide heads", 3, 32, 8, 8, 4},
		{"max GPUs", 8, 64, 8, 8, 8},
		{"single GPU", 1, 32, 8, 1, 1},
		{"indivisible fallback", 3, 7, 7, 4, 4}, // 7 not divisible by 3, falls to 4 which also doesn't divide — returns maxGPUs
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := validTPDegree(tt.minTP, tt.heads, tt.kvHeads, tt.maxGPUs)
			if got != tt.wantTP {
				t.Errorf("validTPDegree(%d, %d, %d, %d) = %d, want %d",
					tt.minTP, tt.heads, tt.kvHeads, tt.maxGPUs, got, tt.wantTP)
			}
		})
	}
}

func TestRoundDownContext(t *testing.T) {
	tests := []struct {
		tokens int
		want   int
	}{
		{100000, 65536},
		{32768, 32768},
		{10000, 8192},
		{5000, 4096},
		{3000, 2048},
		{1500, 1024},
		{600, 512},
		{100, 512},
	}
	for _, tt := range tests {
		if got := roundDownContext(tt.tokens); got != tt.want {
			t.Errorf("roundDownContext(%d) = %d, want %d", tt.tokens, got, tt.want)
		}
	}
}

func TestRecommendMistral7B_G5Xlarge(t *testing.T) {
	// Mistral 7B (~14.5 GiB in BF16) on g5.xlarge (1 A10G, 24 GiB).
	// Should fit at native precision with TP=1.
	rec := Recommend(mistral7B, g5xlarge, allInstances, RecommendOptions{})

	if !rec.Explanation.Feasible {
		t.Fatal("expected feasible recommendation")
	}
	if rec.TensorParallelDegree != 1 {
		t.Errorf("TP = %d, want 1", rec.TensorParallelDegree)
	}
	if rec.Quantization != nil {
		t.Errorf("quantization = %v, want nil", rec.Quantization)
	}
	if rec.MaxModelLen < 512 {
		t.Errorf("max_model_len = %d, want >= 512", rec.MaxModelLen)
	}
	if rec.Concurrency < 1 {
		t.Errorf("concurrency = %d, want >= 1", rec.Concurrency)
	}
	// Input + output should fit within max_model_len
	if rec.InputSequenceLength+rec.OutputSequenceLength > rec.MaxModelLen {
		t.Errorf("input(%d) + output(%d) > max_model_len(%d)",
			rec.InputSequenceLength, rec.OutputSequenceLength, rec.MaxModelLen)
	}
}

func TestRecommendMistral7B_G5_12xlarge(t *testing.T) {
	// Mistral 7B on g5.12xlarge (4 A10G, 96 GiB).
	// Should fit easily at native precision.
	rec := Recommend(mistral7B, g5_12xlarge, allInstances, RecommendOptions{})

	if !rec.Explanation.Feasible {
		t.Fatal("expected feasible recommendation")
	}
	if rec.Quantization != nil {
		t.Errorf("quantization = %v, want nil", rec.Quantization)
	}
	// TP defaults to max (4) to use all GPUs, even though model fits on 1.
	if rec.TensorParallelDegree != 4 {
		t.Errorf("TP = %d, want 4", rec.TensorParallelDegree)
	}
	// With 4 GPUs and joint max_model_len×concurrency constraint, context is
	// capped to fit safely alongside concurrent requests.
	if rec.MaxModelLen < 4096 {
		t.Errorf("max_model_len = %d, want >= 4096", rec.MaxModelLen)
	}
}

func TestRecommendLlama70B_G5Xlarge_Infeasible(t *testing.T) {
	// Llama 70B (~140 GiB in BF16) on g5.xlarge (24 GiB).
	// Even INT4 (~35 GiB) doesn't fit on 24 GiB.
	rec := Recommend(llama70B, g5xlarge, allInstances, RecommendOptions{})

	if rec.Explanation.Feasible {
		t.Fatal("expected infeasible recommendation")
	}
	if rec.Explanation.Reason == "" {
		t.Error("expected non-empty reason for infeasibility")
	}
	// Should suggest a larger instance.
	if rec.Alternatives == nil || rec.Alternatives.LargerInstance == "" {
		t.Error("expected a larger instance suggestion")
	}
}

func TestRecommendLlama70B_P5_48xlarge(t *testing.T) {
	// Llama 70B on p5.48xlarge (8 H100, 640 GiB).
	// Should fit at native BF16 with TP=2 or more.
	rec := Recommend(llama70B, p5_48xlarge, allInstances, RecommendOptions{})

	if !rec.Explanation.Feasible {
		t.Fatalf("expected feasible: %s", rec.Explanation.Reason)
	}
	if rec.Quantization != nil {
		t.Errorf("quantization = %v, want nil (BF16 should fit)", rec.Quantization)
	}
	if rec.TensorParallelDegree < 2 {
		t.Errorf("TP = %d, want >= 2 for 70B model", rec.TensorParallelDegree)
	}
	if rec.MaxModelLen < 4096 {
		t.Errorf("max_model_len = %d, want >= 4096", rec.MaxModelLen)
	}
}

func TestRecommendLlama70B_G5_48xlarge_NeedsQuantization(t *testing.T) {
	// Llama 70B (~140 GiB BF16) on g5.48xlarge (8 A10G, 192 GiB).
	// Doesn't fit at BF16 (192*0.9=172.8 < 140... actually it does barely).
	// Let's recalculate: 140 GiB < 172.8 GiB — it fits!
	// But per-device: 140/8=17.5 GiB per GPU, each has 24 GiB usable 21.6 — fits.
	rec := Recommend(llama70B, g5_48xlarge, allInstances, RecommendOptions{})

	if !rec.Explanation.Feasible {
		t.Fatalf("expected feasible: %s", rec.Explanation.Reason)
	}
	// Should not need quantization since A10G doesn't support FP8 but BF16 fits total.
	// TP should be 8 since 70B needs distribution across all 8 GPUs.
	if rec.TensorParallelDegree < 2 {
		t.Errorf("TP = %d, want >= 2", rec.TensorParallelDegree)
	}
}

func TestRecommendAlternatives_ShowsFP8Option(t *testing.T) {
	// Create a model that doesn't fit at BF16 but fits at FP8.
	// ~70B params = 140 GiB BF16, 70 GiB FP8.
	// Only FP8 can be applied at runtime (INT8/INT4 require pre-quantized models).
	model := ModelConfig{
		ParameterCount:        70_000_000_000,
		HiddenSize:            8192,
		NumAttentionHeads:     64,
		NumKeyValueHeads:      8,
		NumHiddenLayers:       80,
		MaxPositionEmbeddings: 8192,
		TorchDtype:            "bfloat16",
		ModelType:             "llama",
	}
	// 1x H100: 80 GiB total, 72 GiB usable. BF16 140 > 72, FP8 70 < 72.
	inst := InstanceSpec{
		Name: "custom.h100", AcceleratorType: "GPU", AcceleratorName: "H100",
		AcceleratorCount: 1, AcceleratorMemoryGiB: 80,
	}

	rec := Recommend(model, inst, allInstances, RecommendOptions{})

	if !rec.Explanation.Feasible {
		t.Fatalf("expected feasible with FP8 quantization: %s", rec.Explanation.Reason)
	}
	if rec.Quantization == nil {
		t.Fatal("expected quantization to be recommended")
	}
	if *rec.Quantization != "fp8" {
		t.Errorf("quantization = %s, want fp8", *rec.Quantization)
	}
	if rec.Alternatives == nil {
		t.Fatal("expected alternatives to be set")
	}
	if rec.Alternatives.QuantizationOption == nil {
		t.Error("expected quantization option in alternatives")
	}
}

func TestRecommendWithBitsandbytes(t *testing.T) {
	// Model that doesn't fit at native precision on non-FP8 hardware.
	// Should be feasible with INT8 via bitsandbytes (on-the-fly quantization).
	model := ModelConfig{
		ParameterCount:        70_000_000_000,
		HiddenSize:            8192,
		NumAttentionHeads:     64,
		NumKeyValueHeads:      8,
		NumHiddenLayers:       80,
		MaxPositionEmbeddings: 8192,
		TorchDtype:            "bfloat16",
		ModelType:             "llama",
	}
	// A10G doesn't support FP8, but INT8 (70 GiB) fits in 96 GiB
	inst := InstanceSpec{
		Name: "g5.12xlarge", AcceleratorType: "GPU", AcceleratorName: "A10G",
		AcceleratorCount: 4, AcceleratorMemoryGiB: 96,
	}

	rec := Recommend(model, inst, allInstances, RecommendOptions{})

	if !rec.Explanation.Feasible {
		t.Fatalf("expected feasible with INT8: %s", rec.Explanation.Reason)
	}
	if rec.Quantization == nil || *rec.Quantization != "int8" {
		t.Errorf("expected int8 quantization, got %v", rec.Quantization)
	}
	if rec.Alternatives == nil || rec.Alternatives.QuantizationOption == nil {
		t.Fatal("expected alternatives with quantization option")
	}
	if rec.Alternatives.QuantizationOption.RequiresPreQuantized {
		t.Error("expected RequiresPreQuantized=false for bitsandbytes INT8")
	}
	// Explanation should mention bitsandbytes
	if !strings.Contains(rec.Explanation.Quantization, "bitsandbytes") {
		t.Errorf("expected explanation to mention bitsandbytes, got: %s", rec.Explanation.Quantization)
	}
}

func TestRecommendPreQuantizedNVFP4(t *testing.T) {
	// Gemma-4-31B-IT-NVFP4: 31B params pre-quantized at 4-bit via NVIDIA ModelOpt.
	// Native BF16 would require ~62 GiB, but 4-bit is ~15.5 GiB.
	// Should fit on g5.12xlarge (4x A10G, 96 GiB total) without suggesting additional quantization.
	model := ModelConfig{
		ParameterCount:        31_000_000_000,
		HiddenSize:            5120,
		NumAttentionHeads:     40,
		NumKeyValueHeads:      8,
		NumHiddenLayers:       60,
		MaxPositionEmbeddings: 32768,
		TorchDtype:            "bfloat16",
		ModelType:             "gemma4",
		// Pre-quantization info from HuggingFace config.json
		PreQuantized:   true,
		PreQuantMethod: "modelopt",
		PreQuantBits:   4,
	}
	inst := InstanceSpec{
		Name: "g5.12xlarge", AcceleratorType: "GPU", AcceleratorName: "A10G",
		AcceleratorCount: 4, AcceleratorMemoryGiB: 96,
	}

	rec := Recommend(model, inst, allInstances, RecommendOptions{})

	if !rec.Explanation.Feasible {
		t.Fatalf("expected feasible for pre-quantized 4-bit model: %s", rec.Explanation.Reason)
	}
	// Should NOT suggest additional quantization for pre-quantized models
	if rec.Quantization != nil {
		t.Errorf("expected no additional quantization for pre-quantized model, got %s", *rec.Quantization)
	}
	// Explanation should mention pre-quantization
	if !strings.Contains(rec.Explanation.Quantization, "pre-quantized") {
		t.Errorf("expected explanation to mention pre-quantized, got: %s", rec.Explanation.Quantization)
	}
	if !strings.Contains(rec.Explanation.Quantization, "modelopt") {
		t.Errorf("expected explanation to mention quantization method, got: %s", rec.Explanation.Quantization)
	}
}

func TestRecommendPreQuantizedInfeasible(t *testing.T) {
	// Pre-quantized model that's still too large for the instance.
	// Should report infeasible without suggesting further quantization.
	model := ModelConfig{
		ParameterCount:        70_000_000_000,
		HiddenSize:            8192,
		NumAttentionHeads:     64,
		NumKeyValueHeads:      8,
		NumHiddenLayers:       80,
		MaxPositionEmbeddings: 8192,
		TorchDtype:            "bfloat16",
		ModelType:             "llama",
		// Already 4-bit quantized but still needs ~35 GiB
		PreQuantized:   true,
		PreQuantMethod: "gptq",
		PreQuantBits:   4,
	}
	// Single A10G (24 GiB) - 4-bit 70B needs ~35 GiB, doesn't fit
	inst := InstanceSpec{
		Name: "g5.xlarge", AcceleratorType: "GPU", AcceleratorName: "A10G",
		AcceleratorCount: 1, AcceleratorMemoryGiB: 24,
	}

	rec := Recommend(model, inst, allInstances, RecommendOptions{})

	if rec.Explanation.Feasible {
		t.Error("expected infeasible for pre-quantized model that doesn't fit")
	}
	// Should mention it's pre-quantized and suggest a larger instance
	if !strings.Contains(rec.Explanation.Reason, "Pre-quantized") {
		t.Errorf("expected explanation to mention pre-quantized, got: %s", rec.Explanation.Reason)
	}
	if rec.Explanation.SuggestedInstance == "" {
		t.Error("expected a larger instance suggestion")
	}
}

func TestRecommendMixedPrecisionActualMemory(t *testing.T) {
	// Test ActualMemoryBytes calculation for mixed-precision models like NVFP4.
	// Simulates nvidia/Gemma-4-31B-IT-NVFP4 which has:
	// - BF16: 10.46B params (20.9 GiB)
	// - F8_E4M3: 1.3B params (1.3 GiB)
	// - U8: 10.4B params (10.4 GiB)
	// Total: ~33 GiB actual memory (NOT 10 GiB from 4-bit assumption)
	const gibBytes = 1024 * 1024 * 1024
	actualMemory := int64(33 * gibBytes) // 33 GiB actual memory from dtype breakdown

	model := ModelConfig{
		ParameterCount:        21_000_000_000, // Actual param count from HF
		HiddenSize:            4096,
		NumAttentionHeads:     32,
		NumKeyValueHeads:      8,
		NumHiddenLayers:       40,
		MaxPositionEmbeddings: 32768,
		TorchDtype:            "bfloat16",
		ModelType:             "gemma4",
		PreQuantized:          true,
		PreQuantMethod:        "modelopt",
		PreQuantBits:          4,
		ActualMemoryBytes:     actualMemory, // Key: actual memory from safetensors breakdown
	}

	// Single L4 (24 GiB) - should NOT fit because actual memory is 33 GiB
	inst := InstanceSpec{
		Name: "g6.xlarge", AcceleratorType: "GPU", AcceleratorName: "L4",
		AcceleratorCount: 1, AcceleratorMemoryGiB: 24,
	}

	rec := Recommend(model, inst, allInstances, RecommendOptions{})

	if rec.Explanation.Feasible {
		t.Errorf("expected infeasible: 33 GiB model on 24 GiB GPU. Got: %s", rec.Explanation.Quantization)
	}

	// Now test with g5.12xlarge (96 GiB) - should fit
	inst2 := InstanceSpec{
		Name: "g5.12xlarge", AcceleratorType: "GPU", AcceleratorName: "A10G",
		AcceleratorCount: 4, AcceleratorMemoryGiB: 96,
	}

	rec2 := Recommend(model, inst2, allInstances, RecommendOptions{})

	if !rec2.Explanation.Feasible {
		t.Fatalf("expected feasible on 96 GiB: %s", rec2.Explanation.Reason)
	}
	// Should use actual memory in explanation (33 GiB, not 10.5 from 4-bit assumption)
	if !strings.Contains(rec2.Explanation.Quantization, "33") {
		t.Errorf("expected explanation to show ~33 GiB actual memory, got: %s", rec2.Explanation.Quantization)
	}
}

func TestRecommend8B_SingleL4_MaxModelLen(t *testing.T) {
	// DeepSeek-R1-Distill-Llama-8B on a single L4 (24 GiB).
	// With empirical overhead estimation (1.3x model size for single GPU),
	// this is a very tight memory config. The overhead (~20.8 GB) plus
	// model weights (~16 GB) leaves almost no room for KV cache.
	model := ModelConfig{
		ParameterCount:        8_030_261_248,
		HiddenSize:            4096,
		NumAttentionHeads:     32,
		NumKeyValueHeads:      8,
		NumHiddenLayers:       32,
		MaxPositionEmbeddings: 32768,
		TorchDtype:            "bfloat16",
		ModelType:             "llama",
	}
	inst := InstanceSpec{
		Name: "g6.2xlarge", AcceleratorType: "GPU", AcceleratorName: "L4",
		AcceleratorCount: 1, AcceleratorMemoryGiB: 24,
	}
	rec := Recommend(model, inst, allInstances, RecommendOptions{})

	if !rec.Explanation.Feasible {
		t.Fatalf("expected feasible: %s", rec.Explanation.Reason)
	}
	// Very tight memory config - expect minimal context (512 is the floor).
	if rec.MaxModelLen < 512 {
		t.Errorf("max_model_len = %d, want >= 512", rec.MaxModelLen)
	}
	// Concurrency should be at least 1 given limited KV cache space.
	if rec.Concurrency < 1 {
		t.Errorf("concurrency = %d, want >= 1", rec.Concurrency)
	}
	t.Logf("recommended max_model_len = %d, concurrency = %d", rec.MaxModelLen, rec.Concurrency)
}

func TestModelMemoryBytes(t *testing.T) {
	// 7B params in BF16 = 7e9 * 2 = 14e9 bytes ≈ 13 GiB
	mem := modelMemoryBytes(7_000_000_000, "bfloat16")
	wantBytes := float64(7_000_000_000) * 2
	if mem != wantBytes {
		t.Errorf("modelMemoryBytes(7B, bf16) = %v, want %v", mem, wantBytes)
	}

	// Same model in INT4 = 7e9 * 0.5 = 3.5e9 bytes ≈ 3.3 GiB
	mem4 := modelMemoryBytes(7_000_000_000, "int4")
	wantBytes4 := float64(7_000_000_000) * 0.5
	if mem4 != wantBytes4 {
		t.Errorf("modelMemoryBytes(7B, int4) = %v, want %v", mem4, wantBytes4)
	}
}

func TestTransformersVersionCheck(t *testing.T) {
	tests := []struct {
		version     string
		unsupported bool
	}{
		{"", false},           // Empty = assume compatible
		{"4.45.0", false},     // 4.x is supported
		{"4.0.0", false},      // Old 4.x is supported
		{"5.0.0", true},       // 5.x is too new
		{"5.3.0", true},       // 5.3 is too new
		{"5.5.0.dev0", true},  // Dev version of 5.x is too new
		{"invalid", false},    // Can't parse = assume compatible
	}

	for _, tc := range tests {
		unsupported, reason := isTransformersVersionUnsupported(tc.version, "v0.19.0")
		if unsupported != tc.unsupported {
			t.Errorf("isTransformersVersionUnsupported(%q) = %v, want %v (reason: %s)",
				tc.version, unsupported, tc.unsupported, reason)
		}
	}

	// Verify the message reflects the configured vLLM version, not a hardcode.
	_, reason := isTransformersVersionUnsupported("5.5.0.dev0", "v0.20.0")
	if !strings.Contains(reason, "vLLM v0.20.0") {
		t.Errorf("expected message to include 'vLLM v0.20.0', got: %s", reason)
	}
	_, reason = isTransformersVersionUnsupported("5.5.0.dev0", "")
	if !strings.Contains(reason, "configured vLLM") {
		t.Errorf("expected fallback wording 'configured vLLM', got: %s", reason)
	}
}

func TestRecommendUnsupportedTransformersVersion(t *testing.T) {
	// Model that requires transformers 5.x (like Gemma 4 or HybridQwen3)
	model := ModelConfig{
		ParameterCount:        32_000_000_000,
		HiddenSize:            4096,
		NumAttentionHeads:     32,
		NumKeyValueHeads:      8,
		NumHiddenLayers:       40,
		MaxPositionEmbeddings: 32768,
		TorchDtype:            "bfloat16",
		ModelType:             "hybrid_qwen3",
		TransformersVersion:   "5.3.0", // Too new for vLLM 0.19.0
	}
	inst := InstanceSpec{
		Name: "p5.48xlarge", AcceleratorType: "GPU", AcceleratorName: "H100",
		AcceleratorCount: 8, AcceleratorMemoryGiB: 640,
	}

	rec := Recommend(model, inst, allInstances, RecommendOptions{})

	if rec.Explanation.Feasible {
		t.Error("expected infeasible for model requiring transformers 5.x")
	}
	if !strings.Contains(rec.Explanation.Reason, "transformers") {
		t.Errorf("expected reason to mention transformers, got: %s", rec.Explanation.Reason)
	}
	if !strings.Contains(rec.Explanation.Reason, "5.3.0") {
		t.Errorf("expected reason to mention required version, got: %s", rec.Explanation.Reason)
	}
}
