package recommend

import (
	"fmt"
	"math"
)

// NeuronInstanceSpec holds Neuron-specific instance specifications.
type NeuronInstanceSpec struct {
	Name                 string `json:"name"`
	NeuronCoreCount      int    `json:"neuron_core_count"`
	NeuronCoreMemoryGiB  int    `json:"neuron_core_memory_gib"` // per core
	TotalNeuronMemoryGiB int    `json:"total_neuron_memory_gib"`
	Generation           string `json:"generation"` // "inf2", "trn1", "trn2"
}

// SupportedNeuronArchitectures lists model architectures supported by vLLM on Neuron.
var SupportedNeuronArchitectures = []string{
	"llama", "mistral", "mixtral", "qwen2", "gemma", "phi",
}

// IsNeuronSupportedArchitecture checks if a model architecture is supported on Neuron.
func IsNeuronSupportedArchitecture(modelType string) bool {
	for _, arch := range SupportedNeuronArchitectures {
		if modelType == arch {
			return true
		}
	}
	return false
}

// neuronMemoryPerCore returns memory per NeuronCore in GiB based on instance generation.
func neuronMemoryPerCore(generation string) int {
	switch generation {
	case "trn2":
		return 96
	default: // inf2, trn1
		return 16
	}
}

// neuronGeneration infers the Neuron generation from the instance name.
func neuronGeneration(instanceName string) string {
	if len(instanceName) >= 4 {
		prefix := instanceName[:4]
		if prefix == "trn2" {
			return "trn2"
		}
		if prefix == "trn1" {
			return "trn1"
		}
	}
	return "inf2"
}

// isPowerOfTwo checks if n is a power of 2.
func isPowerOfTwo(n int) bool {
	return n > 0 && (n&(n-1)) == 0
}

// nextPowerOfTwo returns the smallest power of 2 >= n.
func nextPowerOfTwo(n int) int {
	if n <= 1 {
		return 1
	}
	p := 1
	for p < n {
		p *= 2
	}
	return p
}

// validNeuronTPDegree finds a valid TP degree for Neuron instances.
// Constraints: must be power of 2 AND divide both num_attention_heads and num_key_value_heads.
// When numKVHeads > 1, we prefer TP >= 2 to actually distribute KV heads and avoid
// the GQA-to-MHA conversion which causes significant memory overhead during compilation.
func validNeuronTPDegree(minTP, numHeads, numKVHeads, maxCores int) int {
	// When model has multiple KV heads, prefer TP >= 2 to distribute them
	// and avoid GQA-to-MHA conversion overhead
	effectiveMin := minTP
	if numKVHeads > 1 && effectiveMin < 2 {
		effectiveMin = 2
	}

	tp := nextPowerOfTwo(effectiveMin)
	for tp <= maxCores {
		if numHeads%tp == 0 && numKVHeads%tp == 0 {
			return tp
		}
		tp *= 2
	}
	// Fallback: return maxCores even if constraints aren't met
	return maxCores
}

// RecommendNeuron computes configuration recommendations for Neuron instances.
// Unlike GPU, Neuron does not support quantization fallback (BF16 only).
func RecommendNeuron(cfg ModelConfig, inst InstanceSpec) *Recommendation {
	generation := neuronGeneration(inst.Name)
	memPerCore := neuronMemoryPerCore(generation)
	totalCores := inst.AcceleratorCount
	totalMemGiB := memPerCore * totalCores
	totalMemBytes := float64(totalMemGiB) * gibBytes
	usableBytes := totalMemBytes * (1 - overheadFraction)

	// Neuron only supports BF16
	dtype := "bfloat16"
	modelMemBytes := modelMemoryBytes(cfg.ParameterCount, dtype)
	minCores := int(math.Ceil(modelMemBytes / (float64(memPerCore) * gibBytes * (1 - overheadFraction))))
	if minCores < 1 {
		minCores = 1
	}

	rec := &Recommendation{
		InputSequenceLength:  512,
		OutputSequenceLength: 256,
		ModelInfo: ModelInfo{
			ParameterCount:        cfg.ParameterCount,
			NativeDtype:           dtype,
			MaxPositionEmbeddings: cfg.MaxPositionEmbeddings,
			Architecture:          cfg.ModelType,
		},
		InstanceInfo: InstanceInfo{
			AcceleratorCount:     totalCores,
			AcceleratorMemoryGiB: totalMemGiB,
			AcceleratorName:      fmt.Sprintf("NeuronCore (%s)", generation),
		},
	}

	// Check architecture support
	if !IsNeuronSupportedArchitecture(cfg.ModelType) {
		rec.Explanation.Feasible = false
		rec.Explanation.Reason = fmt.Sprintf("Model architecture %q is not supported on Neuron. Supported architectures: llama, mistral, mixtral, qwen2, gemma, phi.", cfg.ModelType)
		return rec
	}

	// Check if model fits
	if modelMemBytes > usableBytes {
		rec.Explanation.Feasible = false
		rec.Explanation.Reason = fmt.Sprintf("Model requires %.1f GiB in BF16 but only %.0f GiB available on %s. Neuron does not support quantization fallback.",
			modelMemBytes/gibBytes, usableBytes/gibBytes, inst.Name)
		return rec
	}

	// Calculate TP degree (must be power of 2 for Neuron)
	tp := validNeuronTPDegree(minCores, cfg.NumAttentionHeads, cfg.NumKeyValueHeads, totalCores)
	rec.TensorParallelDegree = tp
	rec.Quantization = nil
	rec.Explanation.TensorParallelDegree = fmt.Sprintf("TP=%d: model requires %.1f GiB across %d NeuronCores (%d GiB each). TP must be power of 2 for Neuron.",
		tp, modelMemBytes/gibBytes, totalCores, memPerCore)
	rec.Explanation.Quantization = "Neuron instances use BF16 precision only (no quantization support)."

	// Calculate max model length
	kvPerToken := kvCachePerTokenBytes(cfg)
	runtimeOverhead := inferenceOverheadBytes(cfg)
	remainingBytes := usableBytes - modelMemBytes - runtimeOverhead
	if remainingBytes < 0 {
		remainingBytes = 0
	}

	maxTokensKV := int(remainingBytes / kvPerToken)
	maxModelLen := cfg.MaxPositionEmbeddings
	if maxTokensKV < maxModelLen {
		maxModelLen = maxTokensKV
	}
	maxModelLen = roundDownContext(maxModelLen)
	rec.MaxModelLen = maxModelLen
	rec.Explanation.MaxModelLen = fmt.Sprintf("%.1f GiB available for KV cache after model weights. Supports up to %d tokens.",
		remainingBytes/gibBytes, maxModelLen)

	// Adjust input/output if needed
	if maxModelLen < rec.InputSequenceLength+rec.OutputSequenceLength {
		rec.InputSequenceLength = maxModelLen * 2 / 3
		rec.OutputSequenceLength = maxModelLen / 3
	}

	// Calculate concurrency
	avgSeqLen := float64(rec.InputSequenceLength + rec.OutputSequenceLength)
	memPerSeq := kvPerToken * avgSeqLen
	if memPerSeq > 0 {
		maxConcurrent := int(remainingBytes / memPerSeq)
		if maxConcurrent > 64 {
			maxConcurrent = 64
		}
		if maxConcurrent < 1 {
			maxConcurrent = 1
		}
		rec.Concurrency = maxConcurrent
	} else {
		rec.Concurrency = 1
	}
	rec.Explanation.Concurrency = fmt.Sprintf("Based on %.1f GiB KV cache memory with %d-token average sequence length.",
		remainingBytes/gibBytes, int(avgSeqLen))

	rec.Explanation.Feasible = true
	return rec
}
