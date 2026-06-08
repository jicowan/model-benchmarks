package runtime

import "strconv"

const neuronImage = "public.ecr.aws/neuron/pytorch-inference-vllm-neuronx:0.13.0-neuronx-py312-sdk2.28.0-ubuntu24.04"

// VLLMneuron implements Runtime for the "vllm-neuron" framework on Neuron instances.
type VLLMneuron struct{}

func (v *VLLMneuron) Name() string          { return "vllm-neuron" }
func (v *VLLMneuron) ContainerName() string  { return "vllm" }
func (v *VLLMneuron) SupportedAccelerators() []string { return []string{"neuron"} }

func (v *VLLMneuron) ResolveImageOverride() string { return "" }

func (v *VLLMneuron) DefaultImage(version, pullThroughRegistry string) string {
	return neuronImage
}

func (v *VLLMneuron) ResolveVersion(tv ToolVersions) string {
	return tv.FrameworkVersion
}

func (v *VLLMneuron) BuildArgs(p ContainerParams) (command []string, args []string) {
	command = []string{"vllm"}
	args = append(args, "serve", p.ModelHfID)
	args = append(args, "--port", "8000")
	args = append(args, "--tensor-parallel-size", strconv.Itoa(p.TensorParallelDegree))
	args = append(args, "--trust-remote-code")
	args = append(args, "--block-size", "32")

	if p.MaxModelLen > 0 {
		args = append(args, "--max-model-len", strconv.Itoa(p.MaxModelLen))
	}
	if p.MaxNumBatchedTokens > 0 {
		args = append(args, "--max-num-batched-tokens", strconv.Itoa(p.MaxNumBatchedTokens))
	}
	if p.MaxNumSeqs > 0 {
		args = append(args, "--max-num-seqs", strconv.Itoa(p.MaxNumSeqs))
	}

	return command, args
}

func (v *VLLMneuron) MapQuantization(quant string, useRunaiStreamer bool) []string {
	return nil
}
