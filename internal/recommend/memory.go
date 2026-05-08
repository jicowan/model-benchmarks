// Package recommend memory.go contains detailed memory estimation formulas
// for vLLM inference workloads.
package recommend

// MemoryBreakdown provides a detailed breakdown of GPU memory usage.
type MemoryBreakdown struct {
	ModelWeightsGiB         float64 `json:"model_weights_gib"`
	KVCacheGiB              float64 `json:"kv_cache_gib"`
	QuantizationMetadataGiB float64 `json:"quantization_metadata_gib"`
	BlockTableGiB           float64 `json:"block_table_gib"`
	RuntimeOverheadGiB      float64 `json:"runtime_overhead_gib"`
	TotalUsedGiB            float64 `json:"total_used_gib"`
	TotalAvailableGiB       float64 `json:"total_available_gib"`
	HeadroomGiB             float64 `json:"headroom_gib"`
}

// quantizationGroupSize is the standard group size for GPTQ/AWQ quantization.
// Group quantization stores scale (and optionally zero-point) values shared
// across groups of consecutive weights.
const quantizationGroupSize = 128

// QuantizationMetadataBytes returns the memory overhead for quantization
// scale and zero-point storage.
//
// INT8 (GPTQ/AWQ): 2 bytes per group (scale only, FP16)
// INT4 (GPTQ/AWQ): 4 bytes per group (scale + zero-point, both FP16)
//
// Formula: (parameter_count / group_size) * bytes_per_group
func QuantizationMetadataBytes(params int64, quant string) float64 {
	numGroups := float64(params) / quantizationGroupSize
	switch quant {
	case "int8":
		// Scale only: 2 bytes (FP16) per group
		return numGroups * 2
	case "int4":
		// Scale + zero-point: 4 bytes (2 × FP16) per group
		return numGroups * 4
	default:
		// FP16, BF16, FP8, FP32 don't use group quantization metadata
		return 0
	}
}

// pagedAttentionBlockSize is vLLM's default block size for PagedAttention.
// Each block holds this many tokens worth of KV cache.
const pagedAttentionBlockSize = 16

// BlockTableBytes returns the memory overhead for PagedAttention block tables.
//
// vLLM uses PagedAttention to manage KV cache in fixed-size blocks. Each
// sequence needs a block table that maps logical token positions to physical
// memory blocks.
//
// Per-sequence overhead:
//   - Block table entries: (max_model_len / block_size) * 4 bytes (int32)
//   - Sequence metadata: ~64 bytes (pointers, counters, etc.)
//
// Formula: concurrency * ((max_model_len / block_size) * 4 + 64)
func BlockTableBytes(maxModelLen, concurrency int) float64 {
	blocksPerSeq := (maxModelLen + pagedAttentionBlockSize - 1) / pagedAttentionBlockSize
	bytesPerEntry := 4  // int32 block index
	metadataPerSeq := 64 // sequence metadata overhead
	return float64(concurrency) * float64(blocksPerSeq*bytesPerEntry+metadataPerSeq)
}

// CalculateMemoryBreakdown computes a detailed memory breakdown given configuration.
// inputSeqLen and outputSeqLen should match the benchmark workload (e.g., 512+256).
func CalculateMemoryBreakdown(
	params int64,
	quant string,
	kvPerTokenBytes float64,
	maxModelLen int,
	inputSeqLen int,
	outputSeqLen int,
	concurrency int,
	overheadBytes float64,
	tpDegree int,
	perDeviceGiB float64,
) MemoryBreakdown {
	modelWeights := modelMemoryBytes(params, quant)
	quantMetadata := QuantizationMetadataBytes(params, quant)
	blockTable := BlockTableBytes(maxModelLen, concurrency)

	// KV cache: based on actual workload sequence length, not max context
	// This matches how Recommend() calculates concurrency
	avgSeqLen := float64(inputSeqLen + outputSeqLen)
	kvCacheBytes := kvPerTokenBytes * avgSeqLen * float64(concurrency)

	totalUsed := modelWeights + quantMetadata + blockTable + kvCacheBytes + overheadBytes
	// vLLM only gets gpu_memory_utilization (0.90) of per-device VRAM;
	// the remaining 10% is reserved for CUDA context + PyTorch allocator
	// overhead that vLLM can't touch. Reporting raw VRAM here made the
	// memory map show phantom headroom users couldn't use.
	totalAvailable := perDeviceGiB * float64(tpDegree) * gibBytes * gpuMemoryUtilization

	return MemoryBreakdown{
		ModelWeightsGiB:         modelWeights / gibBytes,
		KVCacheGiB:              kvCacheBytes / gibBytes,
		QuantizationMetadataGiB: quantMetadata / gibBytes,
		BlockTableGiB:           blockTable / gibBytes,
		RuntimeOverheadGiB:      overheadBytes / gibBytes,
		TotalUsedGiB:            totalUsed / gibBytes,
		TotalAvailableGiB:       totalAvailable / gibBytes,
		HeadroomGiB:             (totalAvailable - totalUsed) / gibBytes,
	}
}
