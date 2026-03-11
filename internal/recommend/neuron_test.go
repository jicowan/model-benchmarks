package recommend

import (
	"testing"
)

// Neuron instance specs for testing.
var (
	inf2xlarge = InstanceSpec{
		Name: "inf2.xlarge", AcceleratorType: "Neuron", AcceleratorName: "Inferentia2",
		AcceleratorCount: 2, AcceleratorMemoryGiB: 32, MemoryGiB: 16, // 2 cores × 16 GiB, 16 GiB host
	}
	inf2_8xlarge = InstanceSpec{
		Name: "inf2.8xlarge", AcceleratorType: "Neuron", AcceleratorName: "Inferentia2",
		AcceleratorCount: 2, AcceleratorMemoryGiB: 32, MemoryGiB: 128, // 128 GiB host
	}
	inf2_24xlarge = InstanceSpec{
		Name: "inf2.24xlarge", AcceleratorType: "Neuron", AcceleratorName: "Inferentia2",
		AcceleratorCount: 12, AcceleratorMemoryGiB: 192, MemoryGiB: 384, // 12 cores × 16 GiB, 384 GiB host
	}
	trn1_2xlarge = InstanceSpec{
		Name: "trn1.2xlarge", AcceleratorType: "Neuron", AcceleratorName: "Trainium",
		AcceleratorCount: 2, AcceleratorMemoryGiB: 32, MemoryGiB: 32, // 2 cores × 16 GiB, 32 GiB host
	}
	trn1_32xlarge = InstanceSpec{
		Name: "trn1.32xlarge", AcceleratorType: "Neuron", AcceleratorName: "Trainium",
		AcceleratorCount: 32, AcceleratorMemoryGiB: 512, MemoryGiB: 512, // 32 cores × 16 GiB, 512 GiB host
	}
	trn2_48xlarge = InstanceSpec{
		Name: "trn2.48xlarge", AcceleratorType: "Neuron", AcceleratorName: "Trainium2",
		AcceleratorCount: 64, AcceleratorMemoryGiB: 6144, MemoryGiB: 768, // 64 cores × 96 GiB, 768 GiB host
	}
)

func TestIsPowerOfTwo(t *testing.T) {
	tests := []struct {
		n    int
		want bool
	}{
		{1, true},
		{2, true},
		{4, true},
		{8, true},
		{16, true},
		{32, true},
		{3, false},
		{5, false},
		{6, false},
		{7, false},
		{12, false},
		{0, false},
		{-1, false},
	}
	for _, tt := range tests {
		if got := isPowerOfTwo(tt.n); got != tt.want {
			t.Errorf("isPowerOfTwo(%d) = %v, want %v", tt.n, got, tt.want)
		}
	}
}

func TestNextPowerOfTwo(t *testing.T) {
	tests := []struct {
		n    int
		want int
	}{
		{0, 1},
		{1, 1},
		{2, 2},
		{3, 4},
		{4, 4},
		{5, 8},
		{7, 8},
		{8, 8},
		{9, 16},
		{12, 16},
		{16, 16},
		{17, 32},
	}
	for _, tt := range tests {
		if got := nextPowerOfTwo(tt.n); got != tt.want {
			t.Errorf("nextPowerOfTwo(%d) = %d, want %d", tt.n, got, tt.want)
		}
	}
}

func TestNeuronGeneration(t *testing.T) {
	tests := []struct {
		name string
		want string
	}{
		{"inf2.xlarge", "inf2"},
		{"inf2.24xlarge", "inf2"},
		{"trn1.2xlarge", "trn1"},
		{"trn1.32xlarge", "trn1"},
		{"trn2.48xlarge", "trn2"},
		{"unknown", "inf2"}, // default
	}
	for _, tt := range tests {
		if got := neuronGeneration(tt.name); got != tt.want {
			t.Errorf("neuronGeneration(%q) = %q, want %q", tt.name, got, tt.want)
		}
	}
}

func TestNeuronMemoryPerCore(t *testing.T) {
	tests := []struct {
		gen  string
		want int
	}{
		{"inf2", 16},
		{"trn1", 16},
		{"trn2", 96},
	}
	for _, tt := range tests {
		if got := neuronMemoryPerCore(tt.gen); got != tt.want {
			t.Errorf("neuronMemoryPerCore(%q) = %d, want %d", tt.gen, got, tt.want)
		}
	}
}

func TestValidNeuronTPDegree(t *testing.T) {
	tests := []struct {
		name     string
		minTP    int
		heads    int
		kvHeads  int
		maxCores int
		wantTP   int
	}{
		{"1 core sufficient but prefer TP>=2 for KV heads", 1, 32, 8, 2, 2},
		{"needs 2, power of 2", 2, 32, 8, 8, 2},
		{"needs 3, roundup to 4", 3, 32, 8, 8, 4},
		{"needs 5, roundup to 8", 5, 64, 8, 8, 8},
		{"max cores", 8, 64, 8, 8, 8},
		{"12 cores, need 8", 6, 64, 8, 12, 8}, // 8 is power of 2 and divides both
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := validNeuronTPDegree(tt.minTP, tt.heads, tt.kvHeads, tt.maxCores)
			if got != tt.wantTP {
				t.Errorf("validNeuronTPDegree(%d, %d, %d, %d) = %d, want %d",
					tt.minTP, tt.heads, tt.kvHeads, tt.maxCores, got, tt.wantTP)
			}
		})
	}
}

func TestIsNeuronSupportedArchitecture(t *testing.T) {
	supported := []string{"llama", "mistral", "mixtral", "qwen2", "gemma", "phi"}
	for _, arch := range supported {
		if !IsNeuronSupportedArchitecture(arch) {
			t.Errorf("expected %q to be supported", arch)
		}
	}

	unsupported := []string{"gpt2", "bert", "falcon", "mamba", ""}
	for _, arch := range unsupported {
		if IsNeuronSupportedArchitecture(arch) {
			t.Errorf("expected %q to be unsupported", arch)
		}
	}
}

func TestRecommendNeuron_Mistral7B_Inf2Xlarge(t *testing.T) {
	// Mistral 7B (~14.5 GiB BF16) on inf2.xlarge (16 GiB host memory).
	// Should be INFEASIBLE due to insufficient host memory for compilation.
	rec := RecommendNeuron(mistral7B, inf2xlarge)

	if rec.Explanation.Feasible {
		t.Fatalf("expected infeasible due to host memory, got feasible")
	}
}

func TestRecommendNeuron_Mistral7B_Inf2_8xlarge(t *testing.T) {
	// Mistral 7B (~14.5 GiB BF16) on inf2.8xlarge (128 GiB host memory).
	// Should fit with TP=2 (power of 2).
	rec := RecommendNeuron(mistral7B, inf2_8xlarge)

	if !rec.Explanation.Feasible {
		t.Fatalf("expected feasible: %s", rec.Explanation.Reason)
	}
	// TP must be power of 2 for Neuron.
	if !isPowerOfTwo(rec.TensorParallelDegree) {
		t.Errorf("TP = %d is not a power of 2", rec.TensorParallelDegree)
	}
	// No quantization on Neuron.
	if rec.Quantization != nil {
		t.Errorf("quantization = %v, want nil (Neuron is BF16 only)", rec.Quantization)
	}
	if rec.MaxModelLen < 512 {
		t.Errorf("max_model_len = %d, want >= 512", rec.MaxModelLen)
	}
}

func TestRecommendNeuron_Llama70B_Inf2Xlarge_Infeasible(t *testing.T) {
	// Llama 70B (~140 GiB BF16) on inf2.xlarge (32 GiB).
	// Should be infeasible (no quantization fallback on Neuron).
	rec := RecommendNeuron(llama70B, inf2xlarge)

	if rec.Explanation.Feasible {
		t.Fatal("expected infeasible recommendation")
	}
	if rec.Explanation.Reason == "" {
		t.Error("expected non-empty reason for infeasibility")
	}
}

func TestRecommendNeuron_Llama70B_Trn1_32xlarge(t *testing.T) {
	// Llama 70B (~140 GiB BF16) on trn1.32xlarge (32 cores × 16 GiB = 512 GiB).
	// Should fit with high TP (power of 2).
	rec := RecommendNeuron(llama70B, trn1_32xlarge)

	if !rec.Explanation.Feasible {
		t.Fatalf("expected feasible: %s", rec.Explanation.Reason)
	}
	if !isPowerOfTwo(rec.TensorParallelDegree) {
		t.Errorf("TP = %d is not a power of 2", rec.TensorParallelDegree)
	}
	// Should use multiple cores.
	if rec.TensorParallelDegree < 4 {
		t.Errorf("TP = %d, want >= 4 for 70B model", rec.TensorParallelDegree)
	}
	if rec.MaxModelLen < 4096 {
		t.Errorf("max_model_len = %d, want >= 4096", rec.MaxModelLen)
	}
}

func TestRecommendNeuron_Llama70B_Trn2_48xlarge(t *testing.T) {
	// Llama 70B on trn2.48xlarge (64 cores × 96 GiB = 6144 GiB).
	// Should fit easily with modest TP.
	rec := RecommendNeuron(llama70B, trn2_48xlarge)

	if !rec.Explanation.Feasible {
		t.Fatalf("expected feasible: %s", rec.Explanation.Reason)
	}
	if !isPowerOfTwo(rec.TensorParallelDegree) {
		t.Errorf("TP = %d is not a power of 2", rec.TensorParallelDegree)
	}
	// With 96 GiB per core, TP=2 should be enough for 70B.
	if rec.TensorParallelDegree > 4 {
		t.Errorf("TP = %d, want <= 4 (Trn2 has 96 GiB/core)", rec.TensorParallelDegree)
	}
	// Large memory → large context.
	if rec.MaxModelLen < 32768 {
		t.Errorf("max_model_len = %d, want >= 32768", rec.MaxModelLen)
	}
}

func TestRecommendNeuron_UnsupportedArchitecture(t *testing.T) {
	// Model with unsupported architecture.
	model := ModelConfig{
		ParameterCount:        7_000_000_000,
		HiddenSize:            4096,
		NumAttentionHeads:     32,
		NumKeyValueHeads:      8,
		NumHiddenLayers:       32,
		MaxPositionEmbeddings: 8192,
		TorchDtype:            "bfloat16",
		ModelType:             "mamba", // Not supported on Neuron
	}

	rec := RecommendNeuron(model, inf2xlarge)

	if rec.Explanation.Feasible {
		t.Fatal("expected infeasible for unsupported architecture")
	}
	if rec.Explanation.Reason == "" {
		t.Error("expected non-empty reason")
	}
}

func TestRecommendNeuron_OutputsArePowerOfTwo(t *testing.T) {
	// Test various model/instance combinations to ensure TP is always power of 2.
	testCases := []struct {
		model ModelConfig
		inst  InstanceSpec
	}{
		{mistral7B, inf2xlarge},
		{mistral7B, trn1_2xlarge},
		{llama70B, trn1_32xlarge},
		{llama70B, trn2_48xlarge},
	}

	for _, tc := range testCases {
		rec := RecommendNeuron(tc.model, tc.inst)
		if rec.Explanation.Feasible && !isPowerOfTwo(rec.TensorParallelDegree) {
			t.Errorf("RecommendNeuron(%s, %s): TP=%d is not power of 2",
				tc.model.ModelType, tc.inst.Name, rec.TensorParallelDegree)
		}
	}
}
