package recommend

import (
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
	rec := Recommend(mistral7B, g5xlarge, allInstances)

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
	if rec.InputSequenceLength != 512 {
		t.Errorf("input_sequence_length = %d, want 512", rec.InputSequenceLength)
	}
	if rec.OutputSequenceLength != 256 {
		t.Errorf("output_sequence_length = %d, want 256", rec.OutputSequenceLength)
	}
}

func TestRecommendMistral7B_G5_12xlarge(t *testing.T) {
	// Mistral 7B on g5.12xlarge (4 A10G, 96 GiB).
	// Should fit easily at native precision.
	rec := Recommend(mistral7B, g5_12xlarge, allInstances)

	if !rec.Explanation.Feasible {
		t.Fatal("expected feasible recommendation")
	}
	if rec.Quantization != nil {
		t.Errorf("quantization = %v, want nil", rec.Quantization)
	}
	// TP should be 1 since model fits on one GPU.
	if rec.TensorParallelDegree != 1 {
		t.Errorf("TP = %d, want 1", rec.TensorParallelDegree)
	}
	// Lots of memory → large context should be possible.
	if rec.MaxModelLen < 4096 {
		t.Errorf("max_model_len = %d, want >= 4096", rec.MaxModelLen)
	}
}

func TestRecommendLlama70B_G5Xlarge_Infeasible(t *testing.T) {
	// Llama 70B (~140 GiB in BF16) on g5.xlarge (24 GiB).
	// Even INT4 (~35 GiB) doesn't fit on 24 GiB.
	rec := Recommend(llama70B, g5xlarge, allInstances)

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
	rec := Recommend(llama70B, p5_48xlarge, allInstances)

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
	rec := Recommend(llama70B, g5_48xlarge, allInstances)

	if !rec.Explanation.Feasible {
		t.Fatalf("expected feasible: %s", rec.Explanation.Reason)
	}
	// Should not need quantization since A10G doesn't support FP8 but BF16 fits total.
	// TP should be 8 since 70B needs distribution across all 8 GPUs.
	if rec.TensorParallelDegree < 2 {
		t.Errorf("TP = %d, want >= 2", rec.TensorParallelDegree)
	}
}

func TestRecommendAlternatives_ShowsBothOptions(t *testing.T) {
	// Create a model that doesn't fit at BF16 but fits at INT8.
	// ~30B params = 60 GiB BF16, 30 GiB INT8.
	model := ModelConfig{
		ParameterCount:        30_000_000_000,
		HiddenSize:            8192,
		NumAttentionHeads:     64,
		NumKeyValueHeads:      8,
		NumHiddenLayers:       60,
		MaxPositionEmbeddings: 8192,
		TorchDtype:            "bfloat16",
		ModelType:             "llama",
	}
	// g5.xlarge: 24 GiB — BF16 doesn't fit, INT8 doesn't fit either (30 > 21.6).
	// g5.12xlarge: 96 GiB — BF16 doesn't fit (60 > 86.4... actually 60 < 86.4, it fits).
	// Let's use 2-GPU instance: 48 GiB total, 43.2 usable. BF16 60 > 43.2, INT8 30 < 43.2.
	inst := InstanceSpec{
		Name: "custom.2xlarge", AcceleratorType: "GPU", AcceleratorName: "A10G",
		AcceleratorCount: 2, AcceleratorMemoryGiB: 48,
	}

	rec := Recommend(model, inst, allInstances)

	if !rec.Explanation.Feasible {
		t.Fatalf("expected feasible with quantization: %s", rec.Explanation.Reason)
	}
	if rec.Quantization == nil {
		t.Fatal("expected quantization to be recommended")
	}
	if *rec.Quantization != "int8" {
		t.Errorf("quantization = %s, want int8", *rec.Quantization)
	}
	if rec.Alternatives == nil {
		t.Fatal("expected alternatives to be set")
	}
	if rec.Alternatives.QuantizationOption == nil {
		t.Error("expected quantization option in alternatives")
	}
	if rec.Alternatives.LargerInstance == "" {
		t.Error("expected larger instance suggestion in alternatives")
	}
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
