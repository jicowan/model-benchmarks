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
}
