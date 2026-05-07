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

func TestHostMemCheck_S3StreamerStillInfeasibleOnSmallInstance(t *testing.T) {
	// Empirical: the Run:ai streamer with concurrency=16 on shard-heavy
	// models (Qwen3-8B has ~399 safetensors shards) keeps nearly as much
	// host RAM resident as the HF loader path during load. Peak ~15.25
	// × 0.9 + 3 = 16.7 GiB, which still exceeds g6.xlarge's 13.6 GiB
	// allocatable. Observed OOMKill at 13 GiB in a live run with
	// --load-format=runai_streamer, concurrency=16.
	rec := Recommend(qwen3_8b, g6xlargeFull, allWithHostMem, RecommendOptions{
		UseS3Streamer: true,
	})
	if rec.Explanation.Feasible {
		t.Fatal("expected infeasible on g6.xlarge even with S3 streamer")
	}
}

func TestHostMemCheck_S3StreamerFitsOnMidInstance(t *testing.T) {
	// Streamer peak ~16.7 GiB fits in g6.2xlarge's 27.2 GiB allocatable.
	rec := Recommend(qwen3_8b, g6_2xlargeFull, allWithHostMem, RecommendOptions{
		UseS3Streamer: true,
	})
	if !rec.Explanation.Feasible {
		t.Fatalf("expected feasible on g6.2xlarge with streamer, got: %s",
			rec.Explanation.Reason)
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

func TestPeakHostMemBytes_Multipliers(t *testing.T) {
	const weightBytes = 10.0 * gibBytes
	hf := peakHostMemBytes(weightBytes, false)
	s3 := peakHostMemBytes(weightBytes, true)

	// HF path ~= 10 × 1.3 + 3 = 16 GiB
	expectHF := 10.0*hfLoaderHostMultiplier*gibBytes + hostMemBufferBytes
	if hf != expectHF {
		t.Errorf("HF peak = %.2f GiB, want %.2f GiB", hf/gibBytes, expectHF/gibBytes)
	}

	// S3 path ~= 10 × 0.9 + 3 = 12 GiB, still lower than HF but not by much
	expectS3 := 10.0*s3StreamerHostMultiplier*gibBytes + hostMemBufferBytes
	if s3 != expectS3 {
		t.Errorf("S3 peak = %.2f GiB, want %.2f GiB", s3/gibBytes, expectS3/gibBytes)
	}
	if s3 >= hf {
		t.Errorf("expected S3 streamer peak (%.2f GiB) < HF peak (%.2f GiB)",
			s3/gibBytes, hf/gibBytes)
	}
}
