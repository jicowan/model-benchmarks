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
	_, args := rt.BuildArgs(ContainerParams{
		ModelHfID:              "meta-llama/Llama-3.1-70B",
		TensorParallelDegree:   8,
		PipelineParallelDegree: 2,
		NodeCount:              2,
		GPUsPerNode:            8,
	})
	joined := strings.Join(args, " ")
	for _, want := range []string{
		"--model meta-llama/Llama-3.1-70B",
		"--tensor-parallel-size 8",
		"--pipeline-parallel-size 2",
		"--trust-remote-code",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("args missing %q; got: %s", want, joined)
		}
	}
}

func TestLLMD_BuildArgs_DefaultsDegreesToOne(t *testing.T) {
	rt := &LLMD{}
	_, args := rt.BuildArgs(ContainerParams{ModelHfID: "m"})
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "--tensor-parallel-size 1") {
		t.Errorf("TP should default to 1; got: %s", joined)
	}
	if !strings.Contains(joined, "--pipeline-parallel-size 1") {
		t.Errorf("PP should default to 1; got: %s", joined)
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
