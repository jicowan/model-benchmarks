package runtime

import (
	"strings"
	"testing"
)

func TestLLMD_Registered(t *testing.T) {
	rt, err := Get("llm-d")
	if err != nil {
		t.Fatalf("llm-d not registered: %v", err)
	}
	if !IsMultiNode(rt) {
		t.Error("llm-d should report IsMultiNode() == true")
	}
	if got := rt.ContainerName(); got != "vllm" {
		t.Errorf("ContainerName = %q, want vllm", got)
	}
	if accels := rt.SupportedAccelerators(); len(accels) != 1 || accels[0] != "gpu" {
		t.Errorf("SupportedAccelerators = %v, want [gpu]", accels)
	}
}

// TestLLMD_ImageAndVersion covers PRD-66 Part 2: the llm-d-aws image tag comes
// from ToolVersions.LLMDVersion (its own release line), NOT FrameworkVersion
// (which for an llm-d run is the bundled vLLM engine version). DefaultImage
// composes the repo + configured tag; an empty version falls back to the pin.
func TestLLMD_ImageAndVersion(t *testing.T) {
	rt := &LLMD{}
	// ResolveVersion reads LLMDVersion, not FrameworkVersion.
	tv := ToolVersions{FrameworkVersion: "v0.19.0", LLMDVersion: "v0.9.0"}
	if got := rt.ResolveVersion(tv); got != "v0.9.0" {
		t.Errorf("ResolveVersion = %q, want v0.9.0 (LLMDVersion, not FrameworkVersion)", got)
	}
	// DefaultImage composes repo + tag.
	if got := rt.DefaultImage("v0.9.0", ""); got != "ghcr.io/llm-d/llm-d-aws:v0.9.0" {
		t.Errorf("DefaultImage(v0.9.0) = %q", got)
	}
	// Empty version → the shipped default pin.
	if got := rt.DefaultImage("", ""); got != "ghcr.io/llm-d/llm-d-aws:"+DefaultLLMDVersion {
		t.Errorf("DefaultImage(\"\") = %q, want default pin", got)
	}
	// PRD-66 Part 2a: with a pull-through registry, the image routes through the
	// GHCR ECR pull-through cache (ghcr prefix maps to ghcr.io).
	if got := rt.DefaultImage("v0.9.0", "123.dkr.ecr.us-east-2.amazonaws.com"); got != "123.dkr.ecr.us-east-2.amazonaws.com/ghcr/llm-d/llm-d-aws:v0.9.0" {
		t.Errorf("DefaultImage with pull-through = %q", got)
	}
	// LLMDImage helper: direct (no pull-through) and cached forms.
	if got := LLMDImage("v1.2.3", ""); got != "ghcr.io/llm-d/llm-d-aws:v1.2.3" {
		t.Errorf("LLMDImage direct = %q", got)
	}
	if got := LLMDImage("v1.2.3", "123.dkr.ecr.us-east-2.amazonaws.com"); got != "123.dkr.ecr.us-east-2.amazonaws.com/ghcr/llm-d/llm-d-aws:v1.2.3" {
		t.Errorf("LLMDImage pull-through = %q", got)
	}
	// Empty version + pull-through → default pin, still cached.
	if got := LLMDImage("", "123.dkr.ecr.us-east-2.amazonaws.com"); got != "123.dkr.ecr.us-east-2.amazonaws.com/ghcr/llm-d/llm-d-aws:"+DefaultLLMDVersion {
		t.Errorf("LLMDImage empty+pull-through = %q", got)
	}
}

func TestSingleNodeRuntimes_NotMultiNode(t *testing.T) {
	for _, name := range []string{"vllm", "vllm-neuron", "sglang"} {
		rt, err := Get(name)
		if err != nil {
			t.Fatalf("Get(%q): %v", name, err)
		}
		if IsMultiNode(rt) {
			t.Errorf("%q must NOT be multi-node", name)
		}
	}
}

func TestLLMD_BuildArgs_EmitsTPAndPP(t *testing.T) {
	rt := &LLMD{}
	command, args := rt.BuildArgs(ContainerParams{
		ModelHfID:              "meta-llama/Llama-3.1-70B",
		TensorParallelDegree:   8,
		PipelineParallelDegree: 2,
		NodeCount:              2,
		GPUsPerNode:            8,
	})
	// BuildArgs returns only model + static tuning flags; command is nil (the
	// deployment template wraps everything in /bin/bash -c and appends the
	// multi-node coordination flags from LWS env).
	if command != nil {
		t.Errorf("command should be nil (template supplies it), got %v", command)
	}
	joined := strings.Join(args, " ")
	if !strings.HasPrefix(joined, "meta-llama/Llama-3.1-70B ") {
		t.Errorf("model should be the first (positional) arg; got: %s", joined)
	}
	if !strings.Contains(joined, "--trust-remote-code") {
		t.Errorf("args missing --trust-remote-code; got: %s", joined)
	}
	// TP/PP and DP flags are NOT emitted by BuildArgs — they come from the
	// template (they depend on LWS runtime env).
	for _, notWant := range []string{"--tensor-parallel-size", "--pipeline-parallel-size", "--data-parallel", "--model "} {
		if strings.Contains(joined, notWant) {
			t.Errorf("BuildArgs should NOT emit %q (template does); got: %s", notWant, joined)
		}
	}
}

func TestLLMD_BuildArgs_ModelIsPositional(t *testing.T) {
	rt := &LLMD{}
	_, args := rt.BuildArgs(ContainerParams{ModelHfID: "m"})
	if len(args) == 0 || args[0] != "m" {
		t.Errorf("model should be the leading positional arg; got: %v", args)
	}
}

func TestLLMD_BuildArgs_S3Streamer(t *testing.T) {
	rt := &LLMD{}
	_, args := rt.BuildArgs(ContainerParams{
		ModelS3URI:           "s3://bucket/model",
		UseRunaiStreamer:     true,
		TensorParallelDegree: 4,
	})
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "--load-format runai_streamer") {
		t.Errorf("expected runai_streamer load-format; got: %s", joined)
	}
	if !strings.Contains(joined, "s3://bucket/model") {
		t.Errorf("expected S3 URI as model; got: %s", joined)
	}
	// No memory-limit set → extra-config is concurrency-only (default 16).
	if !strings.Contains(joined, `{"concurrency":16}`) {
		t.Errorf("expected concurrency-only extra-config; got: %s", joined)
	}
}

// TestLLMD_BuildArgs_StreamerMemoryLimit (PRD-65 Layer 3): a configured
// StreamerMemoryLimitGiB is emitted as memory_limit (bytes) in the
// --model-loader-extra-config JSON.
func TestLLMD_BuildArgs_StreamerMemoryLimit(t *testing.T) {
	rt := &LLMD{}
	_, args := rt.BuildArgs(ContainerParams{
		ModelS3URI:             "s3://bucket/model",
		UseRunaiStreamer:       true,
		StreamerConcurrency:    8,
		StreamerMemoryLimitGiB: 16,
	})
	joined := strings.Join(args, " ")
	// 16 GiB = 17179869184 bytes; concurrency honored.
	if !strings.Contains(joined, `{"concurrency":8,"memory_limit":17179869184}`) {
		t.Errorf("expected concurrency+memory_limit extra-config; got: %s", joined)
	}
}

// TestStreamerExtraConfig covers the shared JSON builder directly.
func TestStreamerExtraConfig(t *testing.T) {
	if got := streamerExtraConfig(ContainerParams{}); got != `{"concurrency":16}` {
		t.Errorf("default = %q", got)
	}
	if got := streamerExtraConfig(ContainerParams{StreamerConcurrency: 4}); got != `{"concurrency":4}` {
		t.Errorf("concurrency-only = %q", got)
	}
	if got := streamerExtraConfig(ContainerParams{StreamerMemoryLimitGiB: 1}); got != `{"concurrency":16,"memory_limit":1073741824}` {
		t.Errorf("with memory_limit = %q", got)
	}
}
