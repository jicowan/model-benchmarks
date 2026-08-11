package recommend

import (
	"strings"
	"testing"
)

// CPU instance fixtures (accelerator_count=0, host DRAM in MemoryGiB).
var (
	r8g16xlarge = InstanceSpec{
		Name: "r8g.16xlarge", AcceleratorType: "cpu", AcceleratorName: "Graviton4",
		AcceleratorCount: 0, AcceleratorMemoryGiB: 0, MemoryGiB: 512,
	}
	r8g48xlarge = InstanceSpec{
		Name: "r8g.48xlarge", AcceleratorType: "cpu", AcceleratorName: "Graviton4",
		AcceleratorCount: 0, AcceleratorMemoryGiB: 0, MemoryGiB: 1536,
	}
	c8g24xlarge = InstanceSpec{
		Name: "c8g.24xlarge", AcceleratorType: "cpu", AcceleratorName: "Graviton4",
		AcceleratorCount: 0, AcceleratorMemoryGiB: 0, MemoryGiB: 192,
	}
)

// llama8B is a representative small model config.
func llama8B() ModelConfig {
	return ModelConfig{
		ParameterCount:        8_000_000_000,
		HiddenSize:            4096,
		NumAttentionHeads:     32,
		NumKeyValueHeads:      8,
		NumHiddenLayers:       32,
		MaxPositionEmbeddings: 131072,
		IntermediateSize:      14336,
		ModelType:             "llama",
		TorchDtype:            "bfloat16",
	}
}

func TestGravitonGeneration(t *testing.T) {
	cases := map[string]string{
		"r8g.16xlarge": "Graviton4",
		"m8g.24xlarge": "Graviton4",
		"c8g.24xlarge": "Graviton4",
		"r7g.16xlarge": "Graviton3",
		"m9g.16xlarge": "Graviton5",
		"c9g.16xlarge": "Graviton5",
	}
	for name, want := range cases {
		if got := gravitonGeneration(name); got != want {
			t.Errorf("gravitonGeneration(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestCPUKVCacheSpaceGiB(t *testing.T) {
	// Fraction of allocatable host RAM, clamped to [min, max].
	if got := CPUKVCacheSpaceGiB(512); got < cpuKVCacheMinGiB || got > cpuKVCacheMaxGiB {
		t.Errorf("512 GiB host → %d GiB KV, out of [%d,%d]", got, cpuKVCacheMinGiB, cpuKVCacheMaxGiB)
	}
	// Huge box is capped.
	if got := CPUKVCacheSpaceGiB(1536); got != cpuKVCacheMaxGiB {
		t.Errorf("1536 GiB host → %d GiB KV, want cap %d", got, cpuKVCacheMaxGiB)
	}
	// Tiny host floors at the minimum.
	if got := CPUKVCacheSpaceGiB(8); got != cpuKVCacheMinGiB {
		t.Errorf("8 GiB host → %d GiB KV, want floor %d", got, cpuKVCacheMinGiB)
	}
}

func TestRecommendCPU_Llama8B_Fits(t *testing.T) {
	rec := RecommendCPU(llama8B(), r8g16xlarge)
	if !rec.Explanation.Feasible {
		t.Fatalf("llama-8B should fit on r8g.16xlarge (512 GiB): %s", rec.Explanation.Reason)
	}
	// TP=1 (single-socket Graviton), dtype bfloat16, no discrete accelerator.
	if rec.TensorParallelDegree != 1 {
		t.Errorf("TP = %d, want 1 (NUMA nodes)", rec.TensorParallelDegree)
	}
	if rec.ModelInfo.NativeDtype != "bfloat16" {
		t.Errorf("NativeDtype = %q, want bfloat16", rec.ModelInfo.NativeDtype)
	}
	if rec.InstanceInfo.AcceleratorCount != 0 {
		t.Errorf("AcceleratorCount = %d, want 0", rec.InstanceInfo.AcceleratorCount)
	}
	if rec.InstanceInfo.AcceleratorName != "Graviton4" {
		t.Errorf("AcceleratorName = %q, want Graviton4", rec.InstanceInfo.AcceleratorName)
	}
	// Memory-bandwidth advisory must be present.
	if !hasWarning(rec.Warnings, "memory-bandwidth") {
		t.Errorf("expected a memory-bandwidth advisory, got %v", rec.Warnings)
	}
}

func TestRecommendCPU_OversizedUnquantized_Refused(t *testing.T) {
	// 70B dense unquantized → refuse (above the ~13B ceiling), steer to quantized.
	cfg := llama8B()
	cfg.ParameterCount = 70_000_000_000
	rec := RecommendCPU(cfg, r8g48xlarge) // plenty of RAM, but unquantized ceiling applies
	if rec.Explanation.Feasible {
		t.Fatal("70B unquantized on CPU should be refused")
	}
	if !strings.Contains(strings.ToLower(rec.Explanation.Reason), "quantiz") {
		t.Errorf("refusal should steer to quantization, got %q", rec.Explanation.Reason)
	}
	if rec.Alternatives == nil || rec.Alternatives.QuantizationOption == nil {
		t.Error("refusal should offer a quantization alternative")
	}
}

func TestRecommendCPU_LargeQuantizedMoE_Fits(t *testing.T) {
	// A large model that would never fit a GPU node, INT4 pre-quantized, on the
	// 1.5 TiB box: total params in DRAM is CPU's strength.
	cfg := llama8B()
	cfg.ParameterCount = 100_000_000_000
	cfg.PreQuantized = true
	cfg.PreQuantBits = 4
	rec := RecommendCPU(cfg, r8g48xlarge)
	if !rec.Explanation.Feasible {
		t.Fatalf("100B INT4 should fit on r8g.48xlarge (1.5 TiB): %s", rec.Explanation.Reason)
	}
	if rec.Quantization == nil || *rec.Quantization != "int4" {
		t.Errorf("Quantization = %v, want int4", rec.Quantization)
	}
}

func TestRecommendCPU_DoesNotFit_SmallHost(t *testing.T) {
	// 70B INT8 (~70 GiB weights) won't fit c8g.24xlarge's 192 GiB once KV +
	// overhead are budgeted... actually it might; use a clearly-too-big case:
	// 100B unquantized would be refused by the ceiling, so test a pre-quantized
	// 100B INT8 (~100 GiB) on the small 192 GiB box which should still fit,
	// and a 400B INT8 that won't.
	cfg := llama8B()
	cfg.ParameterCount = 400_000_000_000
	cfg.PreQuantized = true
	cfg.PreQuantBits = 8
	rec := RecommendCPU(cfg, c8g24xlarge) // 192 GiB
	if rec.Explanation.Feasible {
		t.Fatal("400B INT8 (~400 GiB) must not fit a 192 GiB host")
	}
	if !strings.Contains(strings.ToLower(rec.Explanation.Reason), "host ram") &&
		!strings.Contains(strings.ToLower(rec.Explanation.Reason), "allocatable") {
		t.Errorf("infeasibility should cite host RAM, got %q", rec.Explanation.Reason)
	}
}

func TestRecommendCPU_RejectsEmbeddingModels(t *testing.T) {
	cfg := llama8B()
	cfg.PipelineTag = "feature-extraction"
	rec := RecommendCPU(cfg, r8g16xlarge)
	if rec.Explanation.Feasible {
		t.Error("embedding model should be rejected (inference-perf can't drive it)")
	}
}

func hasWarning(warnings []string, substr string) bool {
	for _, w := range warnings {
		if strings.Contains(strings.ToLower(w), strings.ToLower(substr)) {
			return true
		}
	}
	return false
}
