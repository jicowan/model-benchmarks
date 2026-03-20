package recommend

import (
	"fmt"
	"testing"
)

// Diverse model architectures for validation
var validationModels = []struct {
	name   string
	config ModelConfig
}{
	// Small models (7-8B)
	{"Llama-3.1-8B", ModelConfig{
		ParameterCount: 8_030_261_248, HiddenSize: 4096,
		NumAttentionHeads: 32, NumKeyValueHeads: 8, NumHiddenLayers: 32,
		MaxPositionEmbeddings: 131072, TorchDtype: "bfloat16", ModelType: "llama",
	}},
	{"Mistral-7B", ModelConfig{
		ParameterCount: 7_248_023_552, HiddenSize: 4096,
		NumAttentionHeads: 32, NumKeyValueHeads: 8, NumHiddenLayers: 32,
		MaxPositionEmbeddings: 32768, TorchDtype: "bfloat16", ModelType: "mistral",
	}},
	{"Qwen2.5-7B", ModelConfig{
		ParameterCount: 7_615_616_512, HiddenSize: 3584,
		NumAttentionHeads: 28, NumKeyValueHeads: 4, NumHiddenLayers: 28,
		MaxPositionEmbeddings: 131072, TorchDtype: "bfloat16", ModelType: "qwen2",
	}},
	// Medium models (13-14B)
	{"Llama-2-13B", ModelConfig{
		ParameterCount: 13_015_864_320, HiddenSize: 5120,
		NumAttentionHeads: 40, NumKeyValueHeads: 40, NumHiddenLayers: 40,
		MaxPositionEmbeddings: 4096, TorchDtype: "bfloat16", ModelType: "llama",
	}},
	// Large models (70B)
	{"Llama-3.1-70B", ModelConfig{
		ParameterCount: 70_553_706_496, HiddenSize: 8192,
		NumAttentionHeads: 64, NumKeyValueHeads: 8, NumHiddenLayers: 80,
		MaxPositionEmbeddings: 131072, TorchDtype: "bfloat16", ModelType: "llama",
	}},
	{"Qwen2.5-72B", ModelConfig{
		ParameterCount: 72_706_891_776, HiddenSize: 8192,
		NumAttentionHeads: 64, NumKeyValueHeads: 8, NumHiddenLayers: 80,
		MaxPositionEmbeddings: 131072, TorchDtype: "bfloat16", ModelType: "qwen2",
	}},
	// Very large models (405B)
	{"Llama-3.1-405B", ModelConfig{
		ParameterCount: 405_000_000_000, HiddenSize: 16384,
		NumAttentionHeads: 128, NumKeyValueHeads: 8, NumHiddenLayers: 126,
		MaxPositionEmbeddings: 131072, TorchDtype: "bfloat16", ModelType: "llama",
	}},
	// MHA models (num_kv_heads == num_attention_heads)
	{"Llama-2-7B-MHA", ModelConfig{
		ParameterCount: 6_738_415_616, HiddenSize: 4096,
		NumAttentionHeads: 32, NumKeyValueHeads: 32, NumHiddenLayers: 32,
		MaxPositionEmbeddings: 4096, TorchDtype: "float16", ModelType: "llama",
	}},
	// Small context models
	{"GPT2-XL", ModelConfig{
		ParameterCount: 1_557_611_200, HiddenSize: 1600,
		NumAttentionHeads: 25, NumKeyValueHeads: 25, NumHiddenLayers: 48,
		MaxPositionEmbeddings: 1024, TorchDtype: "float32", ModelType: "gpt2",
	}},
}

// GPU instances for validation
var validationInstances = []InstanceSpec{
	// Single GPU - small memory
	{Name: "g5.xlarge", AcceleratorType: "GPU", AcceleratorName: "A10G", AcceleratorCount: 1, AcceleratorMemoryGiB: 24},
	{Name: "g6.xlarge", AcceleratorType: "GPU", AcceleratorName: "L4", AcceleratorCount: 1, AcceleratorMemoryGiB: 24},
	// Single GPU - large memory
	{Name: "g6e.xlarge", AcceleratorType: "GPU", AcceleratorName: "L40S", AcceleratorCount: 1, AcceleratorMemoryGiB: 48},
	// Multi-GPU - medium
	{Name: "g5.12xlarge", AcceleratorType: "GPU", AcceleratorName: "A10G", AcceleratorCount: 4, AcceleratorMemoryGiB: 96},
	{Name: "g6e.12xlarge", AcceleratorType: "GPU", AcceleratorName: "L40S", AcceleratorCount: 4, AcceleratorMemoryGiB: 192},
	// Multi-GPU - large
	{Name: "p4d.24xlarge", AcceleratorType: "GPU", AcceleratorName: "A100", AcceleratorCount: 8, AcceleratorMemoryGiB: 320},
	{Name: "p5.48xlarge", AcceleratorType: "GPU", AcceleratorName: "H100", AcceleratorCount: 8, AcceleratorMemoryGiB: 640},
}

func TestValidateRecommendationsUniversal(t *testing.T) {
	var failures []string

	for _, m := range validationModels {
		for _, inst := range validationInstances {
			rec := Recommend(m.config, inst, validationInstances, 0)

			// Validate invariants
			if rec.Explanation.Feasible {
				// 1. max_model_len must be positive and <= model's max
				if rec.MaxModelLen <= 0 {
					failures = append(failures, fmt.Sprintf(
						"%s on %s: max_model_len=%d <= 0", m.name, inst.Name, rec.MaxModelLen))
				}
				if rec.MaxModelLen > m.config.MaxPositionEmbeddings {
					failures = append(failures, fmt.Sprintf(
						"%s on %s: max_model_len=%d > max_position_embeddings=%d",
						m.name, inst.Name, rec.MaxModelLen, m.config.MaxPositionEmbeddings))
				}

				// 2. TP must divide attention heads evenly
				if m.config.NumAttentionHeads%rec.TensorParallelDegree != 0 {
					failures = append(failures, fmt.Sprintf(
						"%s on %s: TP=%d doesn't divide num_attention_heads=%d",
						m.name, inst.Name, rec.TensorParallelDegree, m.config.NumAttentionHeads))
				}
				if m.config.NumKeyValueHeads%rec.TensorParallelDegree != 0 {
					failures = append(failures, fmt.Sprintf(
						"%s on %s: TP=%d doesn't divide num_kv_heads=%d",
						m.name, inst.Name, rec.TensorParallelDegree, m.config.NumKeyValueHeads))
				}

				// 3. TP must be <= accelerator count
				if rec.TensorParallelDegree > inst.AcceleratorCount {
					failures = append(failures, fmt.Sprintf(
						"%s on %s: TP=%d > accelerator_count=%d",
						m.name, inst.Name, rec.TensorParallelDegree, inst.AcceleratorCount))
				}

				// 4. Concurrency must be >= 1
				if rec.Concurrency < 1 {
					failures = append(failures, fmt.Sprintf(
						"%s on %s: concurrency=%d < 1", m.name, inst.Name, rec.Concurrency))
				}

				// 5. Input + output sequence length must fit in max_model_len
				if rec.InputSequenceLength+rec.OutputSequenceLength > rec.MaxModelLen {
					failures = append(failures, fmt.Sprintf(
						"%s on %s: input(%d)+output(%d) > max_model_len(%d)",
						m.name, inst.Name, rec.InputSequenceLength, rec.OutputSequenceLength, rec.MaxModelLen))
				}
			}
		}
	}

	if len(failures) > 0 {
		for _, f := range failures {
			t.Error(f)
		}
	}
}

func TestValidateMemoryEstimates(t *testing.T) {
	// For each feasible config, verify memory math is consistent
	for _, m := range validationModels {
		for _, inst := range validationInstances {
			rec := Recommend(m.config, inst, validationInstances, 0)

			if !rec.Explanation.Feasible {
				continue
			}

			// Calculate what we think the memory usage is
			dtype := nativeDtype(m.config)
			quant := dtype
			if rec.Quantization != nil {
				quant = *rec.Quantization
			}

			modelMem := modelMemoryBytes(m.config.ParameterCount, quant)
			perDeviceGiB := float64(inst.AcceleratorMemoryGiB) / float64(inst.AcceleratorCount)
			totalAvailable := perDeviceGiB * float64(rec.TensorParallelDegree) * gibBytes * gpuMemoryUtilization

			// Model weights must fit in available memory
			if modelMem > totalAvailable {
				t.Errorf("%s on %s: model memory (%.1f GiB) > available (%.1f GiB) but marked feasible",
					m.name, inst.Name, modelMem/gibBytes, totalAvailable/gibBytes)
			}
		}
	}
}

func TestPrintRecommendationMatrix(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping matrix print in short mode")
	}

	fmt.Println("\n=== Recommendation Matrix ===")
	fmt.Printf("%-20s | %-15s | %-8s | %-6s | %-6s | %-10s | %-5s\n",
		"Model", "Instance", "Feasible", "Quant", "TP", "MaxLen", "Conc")
	fmt.Println("--------------------+-----------------+----------+--------+--------+------------+------")

	for _, m := range validationModels {
		for _, inst := range validationInstances {
			rec := Recommend(m.config, inst, validationInstances, 0)

			feasible := "Yes"
			quant := "native"
			tp := fmt.Sprintf("%d", rec.TensorParallelDegree)
			maxLen := fmt.Sprintf("%d", rec.MaxModelLen)
			conc := fmt.Sprintf("%d", rec.Concurrency)

			if !rec.Explanation.Feasible {
				feasible = "No"
				quant = "-"
				tp = "-"
				maxLen = "-"
				conc = "-"
			} else if rec.Quantization != nil {
				quant = *rec.Quantization
			}

			fmt.Printf("%-20s | %-15s | %-8s | %-6s | %-6s | %-10s | %-5s\n",
				m.name[:min(20, len(m.name))], inst.Name, feasible, quant, tp, maxLen, conc)
		}
	}
}
