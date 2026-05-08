package recommend

import (
	"strings"
	"testing"
)

// Qwen3-8B-ish config: 8.19B bf16 weights ≈ 15.25 GiB.
// This is the real-world OOM case that motivated the host-memory check.
var qwen3_8b = ModelConfig{
	ParameterCount:        8_190_000_000,
	HiddenSize:            4096,
	NumAttentionHeads:     32,
	NumKeyValueHeads:      8,
	NumHiddenLayers:       36,
	MaxPositionEmbeddings: 32768,
	TorchDtype:            "bfloat16",
	ModelType:             "qwen3",
	Architectures:         []string{"Qwen3ForCausalLM"},
	PipelineTag:           "text-generation",
}

// Instance specs with realistic host-memory values. The recommender's
// host-memory check only runs when MemoryGiB > 0, so these specs differ
// from recommend_test.go's stripped-down ones.
var (
	g6xlargeFull = InstanceSpec{
		Name: "g6.xlarge", AcceleratorType: "gpu", AcceleratorName: "L4",
		AcceleratorCount: 1, AcceleratorMemoryGiB: 24,
		MemoryGiB: 16,
	}
	g6_2xlargeFull = InstanceSpec{
		Name: "g6.2xlarge", AcceleratorType: "gpu", AcceleratorName: "L4",
		AcceleratorCount: 1, AcceleratorMemoryGiB: 24,
		MemoryGiB: 32,
	}
	g6_4xlargeFull = InstanceSpec{
		Name: "g6.4xlarge", AcceleratorType: "gpu", AcceleratorName: "L4",
		AcceleratorCount: 1, AcceleratorMemoryGiB: 24,
		MemoryGiB: 64,
	}
)

var allWithHostMem = []InstanceSpec{g6xlargeFull, g6_2xlargeFull, g6_4xlargeFull}

func TestHostMemCheck_SmallInstanceInfeasibleForMidsizeModel(t *testing.T) {
	// Qwen3-8B bf16 weights ~15.25 GiB. HF loader peak ~19.8 GiB + 3 GiB
	// buffer = ~22.8 GiB. g6.xlarge host allocatable = 16 × 0.85 = 13.6 GiB.
	// Fails. Suggestion should be a larger instance whose host fits.
	rec := Recommend(qwen3_8b, g6xlargeFull, allWithHostMem, RecommendOptions{})

	if rec.Explanation.Feasible {
		t.Fatal("expected infeasible on g6.xlarge")
	}
	if !strings.Contains(rec.Explanation.Reason, "host RAM") {
		t.Errorf("reason should mention host RAM, got: %s", rec.Explanation.Reason)
	}
	if rec.Explanation.SuggestedInstance == "" {
		t.Error("expected a suggested larger instance")
	}
	if rec.Explanation.SuggestedInstance == "g6.xlarge" {
		t.Errorf("suggestion must be strictly larger than current, got %q",
			rec.Explanation.SuggestedInstance)
	}
	// First larger instance that satisfies ~22.8 GiB peak is g6.2xlarge
	// (32 × 0.85 = 27.2 GiB allocatable).
	if rec.Explanation.SuggestedInstance != "g6.2xlarge" {
		t.Errorf("expected g6.2xlarge, got %q", rec.Explanation.SuggestedInstance)
	}
}

func TestHostMemCheck_LargerInstancePasses(t *testing.T) {
	rec := Recommend(qwen3_8b, g6_2xlargeFull, allWithHostMem, RecommendOptions{})
	if !rec.Explanation.Feasible {
		t.Fatalf("expected feasible on g6.2xlarge, got reason: %s",
			rec.Explanation.Reason)
	}
}

func TestHostMemCheck_S3StreamerInfeasibleOnSmallInstanceByDefault(t *testing.T) {
	// PRD-47: the default S3-streamer multiplier is 1.15, calibrated to
	// empirical TP=1 runs (Run:ai streamer with default
	// RUNAI_STREAMER_MEMORY_LIMIT=-1 allocates a full-file buffer, so
	// real peak ≈ weights × ~1.15 + overhead). Math for Qwen3-8B on
	// g6.xlarge: 15.25 × 1.15 + 2 = ~19.5 GiB peak; allocatable = 16 ×
	// 0.85 = 13.6 GiB → correctly infeasible.
	rec := Recommend(qwen3_8b, g6xlargeFull, allWithHostMem, RecommendOptions{
		UseS3Streamer: true,
	})
	if rec.Explanation.Feasible {
		t.Fatalf("expected infeasible on g6.xlarge (host 16 GiB, peak ~19.5 GiB)")
	}
}

func TestHostMemCheck_S3StreamerFitsOnMidInstance(t *testing.T) {
	// Streamer peak ~19.5 GiB fits in g6.2xlarge's 27.2 GiB allocatable.
	rec := Recommend(qwen3_8b, g6_2xlargeFull, allWithHostMem, RecommendOptions{
		UseS3Streamer: true,
	})
	if !rec.Explanation.Feasible {
		t.Fatalf("expected feasible on g6.2xlarge with streamer, got: %s",
			rec.Explanation.Reason)
	}
}

// PRD-47 PR #5: per-family calibration overrides the default multiplier
// for families we have observations on.
func TestHostMemCheck_CalibrationAppliesToMatchingFamily(t *testing.T) {
	// An empirical ratio of 2.0 for qwen3|s3 should push a pair that
	// was feasible at the 1.15 default into infeasible territory on a
	// small host (15.25 × 2.0 + 2 = 32.5 GiB > 27.2 GiB allocatable).
	rec := Recommend(qwen3_8b, g6_2xlargeFull, allWithHostMem, RecommendOptions{
		UseS3Streamer: true,
		ModelFamily:   "qwen3",
		HostMemCalibration: map[string]float64{
			"qwen3|s3": 2.0,
		},
	})
	if rec.Explanation.Feasible {
		t.Fatal("expected infeasible once observed 2.0x ratio replaces the default 1.15x")
	}
	// Non-matching family shouldn't inherit the qwen3 ratio.
	rec2 := Recommend(qwen3_8b, g6_2xlargeFull, allWithHostMem, RecommendOptions{
		UseS3Streamer: true,
		ModelFamily:   "llama",
		HostMemCalibration: map[string]float64{
			"qwen3|s3": 2.0,
		},
	})
	if !rec2.Explanation.Feasible {
		t.Fatal("llama key shouldn't inherit qwen3 calibration")
	}
}

func TestHostMemCheck_SkippedWhenMemoryGiBUnset(t *testing.T) {
	// Older InstanceSpec records in the DB may lack host-memory info.
	// Don't reject conservatively when we can't do the math.
	inst := InstanceSpec{
		Name: "g6.xlarge", AcceleratorType: "gpu", AcceleratorName: "L4",
		AcceleratorCount: 1, AcceleratorMemoryGiB: 24,
		// MemoryGiB left 0
	}
	rec := Recommend(qwen3_8b, inst, nil, RecommendOptions{})
	// Without host-memory info, the check short-circuits and we fall
	// through to the GPU math. 15.25 GiB fits in 21.6 usable GiB of GPU.
	if !rec.Explanation.Feasible {
		t.Fatalf("expected feasible when MemoryGiB=0, got: %s",
			rec.Explanation.Reason)
	}
}

// PRD-47 PR #3: kubelet+system reservations on EKS AL2023 are ~fixed
// absolute (~1 GiB), not proportional. Large hosts should see more of
// their total RAM as allocatable.
func TestHostAllocatableFrac_TieredByHostSize(t *testing.T) {
	small := hostAllocatableFrac(16)
	medium := hostAllocatableFrac(32)
	justBelowThreshold := hostAllocatableFrac(63)
	atThreshold := hostAllocatableFrac(64)
	large := hostAllocatableFrac(1024)

	if small != hostMemAllocatableFracSmall {
		t.Errorf("16 GiB host: got %v, want %v", small, hostMemAllocatableFracSmall)
	}
	if medium != hostMemAllocatableFracSmall {
		t.Errorf("32 GiB host: got %v, want %v", medium, hostMemAllocatableFracSmall)
	}
	if justBelowThreshold != hostMemAllocatableFracSmall {
		t.Errorf("63 GiB host: got %v, want %v (small bucket)", justBelowThreshold, hostMemAllocatableFracSmall)
	}
	if atThreshold != hostMemAllocatableFracLarge {
		t.Errorf("64 GiB host: got %v, want %v (large bucket)", atThreshold, hostMemAllocatableFracLarge)
	}
	if large != hostMemAllocatableFracLarge {
		t.Errorf("1024 GiB host: got %v, want %v", large, hostMemAllocatableFracLarge)
	}
}

// PRD-47 PR #5: calibration overrides defaults when a matching key
// exists, and unseen families keep the defaults.
func TestPeakHostMemBytes_CalibrationOverridesDefault(t *testing.T) {
	const weightBytes = 10.0 * gibBytes

	defaultHF := peakHostMemBytes(weightBytes, false, "", nil)
	defaultS3 := peakHostMemBytes(weightBytes, true, "", nil)

	calib := map[string]float64{
		"qwen3|s3": 2.8,  // observed worst case for shard-heavy Qwen3 TP=4 runs
		"llama|hf": 1.02, // observed lean HF path for Llama
	}

	// Matching key → use the calibration multiplier.
	qwen3S3 := peakHostMemBytes(weightBytes, true, "qwen3", calib)
	wantQwen3 := 10.0*2.8*gibBytes + hostMemBufferBytes
	if qwen3S3 != wantQwen3 {
		t.Errorf("qwen3+s3: got %.2f GiB, want %.2f GiB (2.8×)", qwen3S3/gibBytes, wantQwen3/gibBytes)
	}
	if qwen3S3 <= defaultS3 {
		t.Errorf("qwen3 calibration should raise peak above default (1.15×); got %.2f vs default %.2f",
			qwen3S3/gibBytes, defaultS3/gibBytes)
	}

	llamaHF := peakHostMemBytes(weightBytes, false, "llama", calib)
	wantLlama := 10.0*1.02*gibBytes + hostMemBufferBytes
	if llamaHF != wantLlama {
		t.Errorf("llama+hf: got %.2f GiB, want %.2f GiB (1.02×)", llamaHF/gibBytes, wantLlama/gibBytes)
	}
	if llamaHF >= defaultHF {
		t.Errorf("llama calibration should lower peak vs default (1.08×); got %.2f vs default %.2f",
			llamaHF/gibBytes, defaultHF/gibBytes)
	}

	// Non-matching family → default unchanged.
	mistralHF := peakHostMemBytes(weightBytes, false, "mistral", calib)
	if mistralHF != defaultHF {
		t.Errorf("mistral (uncalibrated): got %.2f GiB, want default %.2f GiB",
			mistralHF/gibBytes, defaultHF/gibBytes)
	}

	// Loader mismatch → default unchanged.
	qwen3HF := peakHostMemBytes(weightBytes, false, "qwen3", calib)
	if qwen3HF != defaultHF {
		t.Errorf("qwen3+hf (only s3 calibrated): got %.2f GiB, want default %.2f GiB",
			qwen3HF/gibBytes, defaultHF/gibBytes)
	}

	// Empty family or nil map → default unchanged.
	if peakHostMemBytes(weightBytes, true, "", calib) != defaultS3 {
		t.Error("empty family should use default")
	}
	if peakHostMemBytes(weightBytes, true, "qwen3", nil) != defaultS3 {
		t.Error("nil calibration map should use default")
	}
}

func TestPeakHostMemBytes_Multipliers(t *testing.T) {
	const weightBytes = 10.0 * gibBytes
	hf := peakHostMemBytes(weightBytes, false, "", nil)
	s3 := peakHostMemBytes(weightBytes, true, "", nil)

	// HF path: 10 × 1.08 + 2 = ~12.8 GiB (matches measured Phi-4 peak within ~5%).
	expectHF := 10.0*hfLoaderHostMultiplier*gibBytes + hostMemBufferBytes
	if hf != expectHF {
		t.Errorf("HF peak = %.2f GiB, want %.2f GiB", hf/gibBytes, expectHF/gibBytes)
	}

	// S3 path: 10 × 1.15 + 2 = ~13.5 GiB. Empirical ratio from 8 TP=1
	// runs; Run:ai streamer at default RUNAI_STREAMER_MEMORY_LIMIT=-1
	// allocates a buffer equal to the full safetensor file, so peak is
	// slightly higher than the HF loader, not lower.
	expectS3 := 10.0*s3StreamerHostMultiplier*gibBytes + hostMemBufferBytes
	if s3 != expectS3 {
		t.Errorf("S3 peak = %.2f GiB, want %.2f GiB", s3/gibBytes, expectS3/gibBytes)
	}
	if s3 <= hf {
		t.Errorf("expected S3 streamer peak (%.2f GiB) > HF peak (%.2f GiB) — streamer allocates full-file buffer by default",
			s3/gibBytes, hf/gibBytes)
	}
}
