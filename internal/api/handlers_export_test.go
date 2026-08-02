package api

import (
	"strings"
	"testing"

	"github.com/accelbench/accelbench/internal/database"
)

// TestGenerateManifest_EmitsAllVLLMFlags is a regression guard: for a
// fully-populated RunExportDetails, the exported manifest must contain
// every vLLM flag AccelBench passes to the runtime model deployment.
// When a new flag is added to internal/manifest/templates/
// model-deployment.yaml.tmpl, the author must also add the flag to this
// test (and to the export template); otherwise exports drift from
// runtime behavior and the manifest stops reproducing what actually
// ran. See PRD-46 "Export" section for the full flag catalog.
func TestGenerateManifest_EmitsAllVLLMFlags(t *testing.T) {
	q := "int8"
	s3 := "s3://accelbench-models-820537372947/models/meta-llama/Llama-3.1-8B-Instruct"
	mnbt := 8192
	kvDtype := "fp8"
	d := &database.RunExportDetails{
		RunID:                "test-run-id",
		ModelHfID:            "meta-llama/Llama-3.1-8B-Instruct",
		ModelS3URI:           &s3,
		InstanceTypeName:     "g6e.xlarge",
		Framework:            "vllm",
		FrameworkVersion:     "v0.20.1",
		TensorParallelDegree: 1,
		Quantization:         &q,
		MaxModelLen:          8192,
		MaxNumBatchedTokens:  &mnbt,
		KVCacheDtype:         &kvDtype,
		Concurrency:          32,
		AcceleratorType:      "gpu",
		AcceleratorName:      "L40S",
		AcceleratorCount:     1,
		AcceleratorMemoryGiB: 48,
		VCPUs:                4,
		MemoryGiB:            32,
		// PRD-50: GetRunExportDetails resolves UseRunaiStreamer from
		// streamer_mode + model_s3_uri. Tests that hand-build the struct
		// set it explicitly.
		UseRunaiStreamer: true,
	}

	out, err := generateManifest(d)
	if err != nil {
		t.Fatalf("generateManifest: %v", err)
	}

	// Flags every export must emit for a full run config. Extend this
	// slice whenever a new vLLM flag is added to the runtime template.
	want := []string{
		`"--model"`,
		`"--port"`,
		`"8000"`,
		`"--tensor-parallel-size"`,
		`"--trust-remote-code"`,
		`"--max-model-len"`,
		`"8192"`,
		`"--max-num-batched-tokens"`,
		`"--kv-cache-dtype"`,
		`"fp8"`,
		`"--load-format"`,
		`"runai_streamer"`,
		`"--model-loader-extra-config"`,
	}
	for _, w := range want {
		if !strings.Contains(out, w) {
			t.Errorf("exported manifest missing %q\n--- rendered manifest ---\n%s", w, out)
		}
	}

	// PRD-51: --max-num-seqs is no longer emitted; vLLM picks its
	// upstream default of 256.
	if strings.Contains(out, `"--max-num-seqs"`) {
		t.Errorf("exported manifest should not emit --max-num-seqs (PRD-51 decoupled it from concurrency):\n%s", out)
	}

	// PRD-53: the export must be portable — users re-applying the YAML
	// outside AccelBench shouldn't need the accelbench.io/dedicated
	// taint on their cluster.
	if strings.Contains(out, "accelbench.io/dedicated") {
		t.Errorf("exported manifest should not include the accelbench.io/dedicated toleration (PRD-53 keeps exports portable):\n%s", out)
	}

	// Headline comment block should surface the full run config so a
	// reader can reproduce the run from the YAML alone.
	wantComments := []string{
		"# Model: meta-llama/Llama-3.1-8B-Instruct",
		"# Weights: " + s3,
		"# Instance: g6e.xlarge",
		"# Tensor Parallel: 1",
		"# Max Model Length: 8192",
		"# Max Num Batched Tokens: 8192",
		"# KV Cache Dtype: fp8",
	}
	for _, w := range wantComments {
		if !strings.Contains(out, w) {
			t.Errorf("exported manifest comment block missing %q", w)
		}
	}
}

// TestGenerateManifest_HFLoaderPath exercises the branch where the run
// loaded weights from HuggingFace (no S3 URI). --load-format runai_streamer
// must NOT appear; the HF token secret must be referenced.
func TestGenerateManifest_HFLoaderPath(t *testing.T) {
	d := &database.RunExportDetails{
		RunID:                "test-run-id-hf",
		ModelHfID:            "meta-llama/Llama-3.1-8B-Instruct",
		InstanceTypeName:     "g6.xlarge",
		Framework:            "vllm",
		FrameworkVersion:     "v0.20.1",
		TensorParallelDegree: 1,
		MaxModelLen:          4096,
		AcceleratorType:      "gpu",
		AcceleratorName:      "L4",
		AcceleratorCount:     1,
		AcceleratorMemoryGiB: 24,
		VCPUs:                4,
		MemoryGiB:            16,
	}

	out, err := generateManifest(d)
	if err != nil {
		t.Fatalf("generateManifest: %v", err)
	}

	if strings.Contains(out, "runai_streamer") {
		t.Error("HF-loader export should not emit --load-format runai_streamer")
	}
	for _, w := range []string{
		`"--model"`,
		`"meta-llama/Llama-3.1-8B-Instruct"`,
		`"--max-model-len"`,
		`name: hf-token`,
	} {
		if !strings.Contains(out, w) {
			t.Errorf("HF-loader export missing %q", w)
		}
	}
}

// PRD-50: export reproduces the user's streamer knobs — concurrency and
// memory limit must appear verbatim. Previously these were hardcoded.
func TestGenerateManifest_StreamerKnobs(t *testing.T) {
	s3 := "s3://bucket/models/foo"
	conc := 8
	memLimit := 12
	d := &database.RunExportDetails{
		RunID:                  "test-run-streamer",
		ModelHfID:              "mistralai/Mistral-7B-Instruct-v0.3",
		ModelS3URI:             &s3,
		InstanceTypeName:       "g5.4xlarge",
		Framework:              "vllm",
		FrameworkVersion:       "v0.20.1",
		TensorParallelDegree:   1,
		MaxModelLen:            8192,
		AcceleratorType:        "gpu",
		AcceleratorName:        "A10G",
		AcceleratorCount:       1,
		AcceleratorMemoryGiB:   24,
		VCPUs:                  16,
		MemoryGiB:              64,
		StreamerConcurrency:    &conc,
		StreamerMemoryLimitGiB: &memLimit,
		UseRunaiStreamer:       true,
	}
	out, err := generateManifest(d)
	if err != nil {
		t.Fatalf("generateManifest: %v", err)
	}
	// Concurrency must be the user's 8, not the old hardcoded 16.
	if !strings.Contains(out, `"concurrency":8`) {
		t.Errorf("export missing concurrency=8 in model-loader-extra-config:\n%s", out)
	}
	// Memory-limit env var rendered in bytes (12 GiB = 12884901888).
	if !strings.Contains(out, "RUNAI_STREAMER_MEMORY_LIMIT") {
		t.Error("export missing RUNAI_STREAMER_MEMORY_LIMIT env var")
	}
	if !strings.Contains(out, `"12884901888"`) {
		t.Errorf("export missing 12 GiB (12884901888) byte value:\n%s", out)
	}
}

// PRD-50: streamer_mode=off on an S3-backed run must not emit the
// runai_streamer args — vLLM's default loader is used even for S3.
func TestGenerateManifest_StreamerOff(t *testing.T) {
	s3 := "s3://bucket/models/foo"
	d := &database.RunExportDetails{
		RunID:                "test-run-streamer-off",
		ModelHfID:            "mistralai/Mistral-7B-Instruct-v0.3",
		ModelS3URI:           &s3,
		InstanceTypeName:     "g5.4xlarge",
		Framework:            "vllm",
		FrameworkVersion:     "v0.20.1",
		TensorParallelDegree: 1,
		MaxModelLen:          8192,
		AcceleratorType:      "gpu",
		AcceleratorName:      "A10G",
		AcceleratorCount:     1,
		AcceleratorMemoryGiB: 24,
		VCPUs:                16,
		MemoryGiB:            64,
		UseRunaiStreamer:     false, // streamer_mode=off
	}
	out, err := generateManifest(d)
	if err != nil {
		t.Fatalf("generateManifest: %v", err)
	}
	if strings.Contains(out, "runai_streamer") {
		t.Errorf("streamer-off export should not emit --load-format runai_streamer:\n%s", out)
	}
	if strings.Contains(out, "RUNAI_STREAMER_MEMORY_LIMIT") {
		t.Error("streamer-off export should not set RUNAI_STREAMER_MEMORY_LIMIT")
	}
	// The S3 URI should still be passed as the model path.
	if !strings.Contains(out, s3) {
		t.Errorf("streamer-off export missing model S3 URI:\n%s", out)
	}
}

// TestGenerateManifest_DisaggregatedReproducesAppliedConfig (PRD-61/64): the
// exported disaggregated manifest must reproduce what was APPLIED for the run —
// the user's EPP routing overrides AND per-role scheduler overrides — not fixed
// defaults.
func TestGenerateManifest_DisaggregatedReproducesAppliedConfig(t *testing.T) {
	mode := "disaggregated"
	nc := 128
	pcw, qsw, mpb, lru := 5, 3, 512, 99999
	pR, pTP, dR, dTP := 1, 1, 1, 1
	pMax, dMax := 16384, 2048
	net := "tcp"
	d := &database.RunExportDetails{
		RunID:                "pd-run",
		ModelHfID:            "Qwen/Qwen2.5-1.5B-Instruct",
		InstanceTypeName:     "g6.2xlarge",
		Framework:            "llm-d",
		TensorParallelDegree: 1,
		MaxModelLen:          4096,
		AcceleratorType:      "gpu",
		AcceleratorName:      "L4",
		AcceleratorCount:     1,
		AcceleratorMemoryGiB: 24,
		VCPUs:                8,
		MemoryGiB:            32,
		DeploymentMode:       &mode,
		NetworkMode:          &net,
		PrefillReplicas:      &pR,
		PrefillTP:            &pTP,
		DecodeReplicas:       &dR,
		DecodeTP:             &dTP,
		// PRD-64 per-role scheduler overrides.
		PrefillMaxNumBatchedTokens: &pMax,
		DecodeMaxNumBatchedTokens:  &dMax,
		// PRD-61 routing config.
		PDNonCachedTokens:      &nc,
		PDPrefixCacheWeight:    &pcw,
		PDQueueScorerWeight:    &qsw,
		PDMaxPrefixBlocks:      &mpb,
		PDLRUCapacityPerServer: &lru,
	}
	out, err := generateManifest(d)
	if err != nil {
		t.Fatalf("generateManifest: %v", err)
	}
	// PRD-61 routing config as applied (NOT the defaults 16/2/1/256/31250).
	for _, want := range []string{
		"nonCachedTokens: 128",
		"maxPrefixBlocksToMatch: 512",
		"lruCapacityPerServer: 99999",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("export missing applied routing value %q", want)
		}
	}
	if n := strings.Count(out, "weight: 5"); n != 2 {
		t.Errorf("applied prefix-cache weight 5 should appear in both profiles (2), got %d", n)
	}
	// PRD-64 per-role scheduler: prefill 16384, decode 2048 both present.
	if !strings.Contains(out, `"16384"`) || !strings.Contains(out, `"2048"`) {
		t.Errorf("export missing per-role max-num-batched-tokens (16384 prefill / 2048 decode)")
	}
	// The shipped defaults must NOT leak in.
	if strings.Contains(out, "nonCachedTokens: 16\n") {
		t.Error("export used default nonCachedTokens instead of the applied 128")
	}
}

// TestGenerateManifest_DisaggregatedBothPool_ReproducesAppliedConfig (PRD-63/64):
// a D/P run WITH a co-located "both" pool exports the both role + its per-role
// scheduler override, and — because a both pool is present — the anti-self-route
// prefill-only-filter. Covers the both-pool topology with user-supplied values.
func TestGenerateManifest_DisaggregatedBothPool_ReproducesAppliedConfig(t *testing.T) {
	mode := "disaggregated"
	net := "tcp"
	pR, pTP := 1, 1
	bR, bTP := 2, 1
	bMax := 6144 // distinct from max-model-len so the assertion is unambiguous
	d := &database.RunExportDetails{
		RunID:                "pd-both-run",
		ModelHfID:            "Qwen/Qwen2.5-1.5B-Instruct",
		InstanceTypeName:     "g6.2xlarge",
		Framework:            "llm-d",
		TensorParallelDegree: 1,
		MaxModelLen:          4096,
		AcceleratorType:      "gpu",
		AcceleratorName:      "L4",
		AcceleratorCount:     1,
		VCPUs:                8,
		MemoryGiB:            32,
		DeploymentMode:       &mode,
		NetworkMode:          &net,
		// prefill + both (decode covered by the both pool) — a valid PRD-63 combo.
		PrefillReplicas:         &pR,
		PrefillTP:               &pTP,
		BothReplicas:            &bR,
		BothTP:                  &bTP,
		BothMaxNumBatchedTokens: &bMax,
	}
	out, err := generateManifest(d)
	if err != nil {
		t.Fatalf("generateManifest: %v", err)
	}
	// The both role Deployment + its canonical wire label render.
	for _, want := range []string{
		"pd-qwen-qwen2-5-1-5b-instruct-both",
		"llm-d.ai/role: prefill-decode",
		// PRD-63 anti-self-route filter is present because a both pool exists.
		"prefill-only-filter",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("both-pool export missing %q", want)
		}
	}
	// The both role's per-role --max-num-batched-tokens override (6144) is applied.
	// (Shared MaxNumBatchedTokens is nil, so this flag appears ONLY via the both
	// override — an unambiguous check.)
	if !strings.Contains(out, "--max-num-batched-tokens") || !strings.Contains(out, `"6144"`) {
		t.Error("both-pool export missing the applied both_max_num_batched_tokens=6144")
	}
}

// TestGenerateManifest_SGLangSingleNode_ReproducesAppliedConfig: an SGLang
// single-node run must export an SGLang manifest (image + sglang.launch_server)
// carrying the user-supplied vLLM-equivalent knobs AND the SGLang-specific
// scheduler knobs (--chunked-prefill-size / --mem-fraction-static) — NOT a vLLM
// manifest. Reuses the runtime BuildArgs so the flags match what ran.
func TestGenerateManifest_SGLangSingleNode_ReproducesAppliedConfig(t *testing.T) {
	cp := 4096
	mf := 0.85
	q := "fp8"
	d := &database.RunExportDetails{
		RunID:                "sgl-run",
		ModelHfID:            "meta-llama/Llama-3.1-8B-Instruct",
		InstanceTypeName:     "g6.2xlarge",
		Framework:            "sglang",
		FrameworkVersion:     "v0.4.1",
		TensorParallelDegree: 2,
		MaxModelLen:          8192,
		Quantization:         &q,
		ChunkedPrefillSize:   &cp,
		MemFractionStatic:    &mf,
		AcceleratorType:      "gpu",
		AcceleratorName:      "L4", // non-Hopper → triton backend
		AcceleratorCount:     2,
		VCPUs:                8,
		MemoryGiB:            32,
	}
	out, err := generateManifest(d)
	if err != nil {
		t.Fatalf("generateManifest: %v", err)
	}
	// SGLang image + launcher, NOT vLLM.
	if !strings.Contains(out, "lmsysorg/sglang:v0.4.1") {
		t.Error("SGLang export must use the sglang image")
	}
	if strings.Contains(out, "vllm/vllm-openai") {
		t.Error("SGLang export must NOT use the vLLM image")
	}
	if !strings.Contains(out, "sglang.launch_server") {
		t.Error("SGLang export must launch sglang.launch_server")
	}
	// Applied knobs (vLLM-equivalent + SGLang-specific).
	for _, want := range []string{
		`"--tp-size"`, `"2"`,
		`"--context-length"`, `"8192"`,
		`"--chunked-prefill-size"`, `"4096"`,
		`"--mem-fraction-static"`, `"0.85"`,
		`"--quantization"`, `"fp8"`,
		`"--attention-backend"`, `"triton"`, // L4 = non-Hopper
	} {
		if !strings.Contains(out, want) {
			t.Errorf("SGLang export missing applied flag %q", want)
		}
	}
	// Container named sglang; still a plain single-node Deployment (no llm-d graph).
	if !strings.Contains(out, "- name: sglang") {
		t.Error("SGLang container should be named sglang")
	}
	if strings.Contains(out, "kind: LeaderWorkerSet") || strings.Contains(out, "kind: InferencePool") {
		t.Error("SGLang single-node export must be a plain Deployment")
	}
}

// TestGenerateManifest_SGLangNoSchedulerKnobs: an SGLang run that set no
// scheduler knobs omits --chunked-prefill-size / --mem-fraction-static (they're
// optional; absence = SGLang default).
func TestGenerateManifest_SGLangNoSchedulerKnobs(t *testing.T) {
	d := &database.RunExportDetails{
		ModelHfID:            "meta-llama/Llama-3.1-8B-Instruct",
		InstanceTypeName:     "g6.2xlarge",
		Framework:            "sglang",
		FrameworkVersion:     "v0.4.1",
		TensorParallelDegree: 1,
		AcceleratorType:      "gpu",
		AcceleratorName:      "H100", // Hopper → no forced triton backend
		AcceleratorCount:     1,
		VCPUs:                8,
		MemoryGiB:            32,
	}
	out, err := generateManifest(d)
	if err != nil {
		t.Fatalf("generateManifest: %v", err)
	}
	if strings.Contains(out, "--chunked-prefill-size") || strings.Contains(out, "--mem-fraction-static") {
		t.Error("unset SGLang scheduler knobs should not render")
	}
	// Hopper keeps SGLang's default backend (no forced triton).
	if strings.Contains(out, "--attention-backend") {
		t.Error("Hopper GPU should not force the triton attention backend")
	}
}
