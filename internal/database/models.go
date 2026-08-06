package database

import (
	"time"
)

type Model struct {
	ID             string    `json:"id"`
	HfID           string    `json:"hf_id"`
	HfRevision     string    `json:"hf_revision"`
	// ModelType is HuggingFace's canonical architecture name from
	// config.json (e.g. "llama", "qwen2", "qwen3", "phi3", "mistral",
	// "gpt_oss"). Used as the family key for PRD-47 per-family host-
	// memory calibration. Nullable for rows where the config was never
	// fetched.
	ModelType      *string   `json:"model_type,omitempty"`
	ParameterCount *int64    `json:"parameter_count,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
}

type InstanceType struct {
	ID                   string `json:"id"`
	Name                 string `json:"name"`
	Family               string `json:"family"`
	AcceleratorType      string `json:"accelerator_type"`
	AcceleratorName      string `json:"accelerator_name"`
	AcceleratorCount     int    `json:"accelerator_count"`
	AcceleratorMemoryGiB int    `json:"accelerator_memory_gib"`
	VCPUs                int    `json:"vcpus"`
	MemoryGiB            int    `json:"memory_gib"`
}

type BenchmarkRun struct {
	ID                    string     `json:"id"`
	ModelID               string     `json:"model_id"`
	InstanceTypeID        string     `json:"instance_type_id"`
	Framework             string     `json:"framework"`
	FrameworkVersion      string     `json:"framework_version"`
	TensorParallelDegree  int        `json:"tensor_parallel_degree"`
	// PRD-57: distributed-run topology (migration 035). Null on single-instance
	// and historical rows. DeploymentMode distinguishes single vs distributed;
	// NodeCount + PipelineParallelDegree describe the multi-node shape (TP reuses
	// TensorParallelDegree as within-node TP); NetworkMode is the fabric.
	DeploymentMode         *string `json:"deployment_mode,omitempty"`
	NodeCount              *int    `json:"node_count,omitempty"`
	PipelineParallelDegree *int    `json:"pipeline_parallel_degree,omitempty"`
	NetworkMode            *string `json:"network_mode,omitempty"`
	// PRD-58: prefill/decode disaggregation (migration 036). Non-null only when
	// DeploymentMode == "disaggregated". Per-role replica counts + parallelism
	// (TP within-node, PP across-node, independent knobs); KV describes the
	// transfer connector/backend. Null on single, co-located distributed, and
	// historical rows.
	PrefillReplicas   *int    `json:"prefill_replicas,omitempty"`
	PrefillTP         *int    `json:"prefill_tp,omitempty"`
	PrefillPP         *int    `json:"prefill_pp,omitempty"`
	DecodeReplicas    *int    `json:"decode_replicas,omitempty"`
	DecodeTP          *int    `json:"decode_tp,omitempty"`
	DecodePP          *int    `json:"decode_pp,omitempty"`
	// PRD-63: co-located prefill+decode ("both") pool (migration 040). Non-null
	// only when the disaggregated run set a both pool. both_pp is intentionally
	// omitted (per-role PP > 1 is a non-goal).
	BothReplicas *int `json:"both_replicas,omitempty"`
	BothTP       *int `json:"both_tp,omitempty"`
	KVConnector       *string `json:"kv_connector,omitempty"`
	KVTransferBackend *string `json:"kv_transfer_backend,omitempty"`
	// PRD-64: per-role scheduler override (D/P only). Null ⇒ that role uses the
	// shared MaxNumBatchedTokens (below). Prefill wants a large batched-token
	// budget (compute-bound); decode less so (memory-bound). PRD-63 adds the
	// symmetric knob for the co-located "both" role.
	PrefillMaxNumBatchedTokens *int `json:"prefill_max_num_batched_tokens,omitempty"`
	DecodeMaxNumBatchedTokens  *int `json:"decode_max_num_batched_tokens,omitempty"`
	BothMaxNumBatchedTokens    *int `json:"both_max_num_batched_tokens,omitempty"`
	// PRD-61: run-tunable EPP routing config (disaggregated only, migration 041).
	// Null ⇒ the shipped default was used (self-describing run). PDNonCachedTokens
	// null ⇒ default 16; a stored 0 ⇒ disaggregation disabled for the run.
	PDNonCachedTokens       *int    `json:"pd_noncached_tokens,omitempty"`
	PDPrefixCacheWeight     *int    `json:"pd_prefix_cache_weight,omitempty"`
	PDQueueScorerWeight     *int    `json:"pd_queue_scorer_weight,omitempty"`
	PDMaxPrefixBlocks       *int    `json:"pd_max_prefix_blocks,omitempty"`
	PDLRUCapacityPerServer  *int    `json:"pd_lru_capacity_per_server,omitempty"`
	PDDeciderStrategy       *string `json:"pd_decider_strategy,omitempty"`
	Quantization          *string    `json:"quantization,omitempty"`
	Concurrency           int        `json:"concurrency"`
	InputSequenceLength   int        `json:"input_sequence_length"`
	OutputSequenceLength  int        `json:"output_sequence_length"`
	DatasetName           string     `json:"dataset_name"`
	RunType               string     `json:"run_type"`
	MaxModelLen           int        `json:"max_model_len,omitempty"`
	// PRD-46: vLLM scheduler knobs persisted so a run can be reproduced
	// byte-for-byte from the DB. Null on historical rows; the exporter
	// and UI treat null as "flag omitted, vLLM picked its default."
	MaxNumBatchedTokens   *int       `json:"max_num_batched_tokens,omitempty"`
	KVCacheDtype          *string    `json:"kv_cache_dtype,omitempty"`
	// SGLang scheduler knobs. Null on non-SGLang runs and historical rows.
	ChunkedPrefillSize  *int     `json:"chunked_prefill_size,omitempty"`
	MemFractionStatic   *float64 `json:"mem_fraction_static,omitempty"`
	// PRD-50: Run:ai model streamer knobs. Null on historical rows.
	// streamer_mode "off" disables the streamer even for S3 models.
	// streamer_concurrency 0 means "use default (16)".
	// streamer_memory_limit_gib 0 means "auto-size from instance RAM".
	StreamerMode           *string `json:"streamer_mode,omitempty"`
	StreamerConcurrency    *int    `json:"streamer_concurrency,omitempty"`
	StreamerMemoryLimitGiB *int    `json:"streamer_memory_limit_gib,omitempty"`
	ScenarioID            *string    `json:"scenario_id,omitempty"`    // scenario identifier (chatbot, batch, etc.)
	LoadgenConfig         *string    `json:"loadgen_config,omitempty"` // inference-perf YAML config
	ModelS3URI            *string    `json:"model_s3_uri,omitempty"`   // s3://bucket/path — set when weights loaded via Run:ai streamer
	Status                string     `json:"status"`
	ErrorMessage          *string    `json:"error_message,omitempty"`
	Superseded            bool       `json:"superseded"`
	StartedAt             *time.Time `json:"started_at,omitempty"`
	LoadgenStartedAt      *time.Time `json:"loadgen_started_at,omitempty"`
	CompletedAt           *time.Time `json:"completed_at,omitempty"`
	CreatedAt             time.Time  `json:"created_at"`
	// PRD-35: cost frozen at run completion. total_cost_usd covers the full
	// EC2 node lifetime (pull + load + bench + teardown); loadgen_cost_usd
	// covers just the inference-perf window. NULL on historical rows and on
	// completions where pricing lookup failed — aggregates COALESCE to $0.
	TotalCostUSD   *float64 `json:"total_cost_usd,omitempty"`
	LoadgenCostUSD *float64 `json:"loadgen_cost_usd,omitempty"`
	// PRD-40: replica coordination. OwnerPod is the API pod orchestrating
	// this run; CancelRequested is the cross-pod cancel flag polled by the
	// owning pod's goroutine.
	OwnerPod        *string `json:"owner_pod,omitempty"`
	CancelRequested bool    `json:"cancel_requested"`
	// PRD-47: peak container workingSetBytes observed during the load
	// phase, in GiB. Powers per-family host-memory calibration. Null on
	// historical rows and on runs where the kubelet scrape failed.
	HostMemoryPeakGiB *float64 `json:"host_memory_peak_gib,omitempty"`
}

type BenchmarkMetrics struct {
	ID                       string   `json:"id"`
	RunID                    string   `json:"run_id"`
	TTFTP50Ms                *float64 `json:"ttft_p50_ms,omitempty"`
	TTFTP90Ms                *float64 `json:"ttft_p90_ms,omitempty"`
	TTFTP95Ms                *float64 `json:"ttft_p95_ms,omitempty"`
	TTFTP99Ms                *float64 `json:"ttft_p99_ms,omitempty"`
	E2ELatencyP50Ms          *float64 `json:"e2e_latency_p50_ms,omitempty"`
	E2ELatencyP90Ms          *float64 `json:"e2e_latency_p90_ms,omitempty"`
	E2ELatencyP95Ms          *float64 `json:"e2e_latency_p95_ms,omitempty"`
	E2ELatencyP99Ms          *float64 `json:"e2e_latency_p99_ms,omitempty"`
	ITLP50Ms                 *float64 `json:"itl_p50_ms,omitempty"`
	ITLP90Ms                 *float64 `json:"itl_p90_ms,omitempty"`
	ITLP95Ms                 *float64 `json:"itl_p95_ms,omitempty"`
	ITLP99Ms                 *float64 `json:"itl_p99_ms,omitempty"`
	ThroughputPerRequestTPS  *float64 `json:"throughput_per_request_tps,omitempty"`
	ThroughputAggregateTPS   *float64 `json:"throughput_aggregate_tps,omitempty"`
	RequestsPerSecond        *float64 `json:"requests_per_second,omitempty"`
	AcceleratorUtilizationPct    *float64 `json:"accelerator_utilization_pct,omitempty"`
	AcceleratorUtilizationAvgPct *float64 `json:"accelerator_utilization_avg_pct,omitempty"`
	AcceleratorMemoryPeakGiB    *float64 `json:"accelerator_memory_peak_gib,omitempty"`
	// PRD-59: honest group GPU memory total (sum of per-node peaks) for
	// distributed runs. NULL on single-instance + historical rows — for those
	// the existing peak column already answers "how much GPU memory".
	AcceleratorMemoryTotalGiB *float64 `json:"accelerator_memory_total_gib,omitempty"`
	WaitingRequestsMax          *int     `json:"waiting_requests_max,omitempty"`
	SuccessfulRequests          *int     `json:"successful_requests,omitempty"`
	FailedRequests           *int     `json:"failed_requests,omitempty"`
	TotalDurationSeconds     *float64 `json:"total_duration_seconds,omitempty"`
	CreatedAt                time.Time `json:"created_at"`

	// Extended metrics (PRD-14)
	TPOTP50Ms                 *float64 `json:"tpot_p50_ms,omitempty"`
	TPOTP90Ms                 *float64 `json:"tpot_p90_ms,omitempty"`
	TPOTP99Ms                 *float64 `json:"tpot_p99_ms,omitempty"`
	PrefillTimeP50Ms          *float64 `json:"prefill_time_p50_ms,omitempty"`
	DecodeTimeP50Ms           *float64 `json:"decode_time_p50_ms,omitempty"`
	QueueTimeP50Ms            *float64 `json:"queue_time_p50_ms,omitempty"`
	PromptThroughputTPS       *float64 `json:"prompt_throughput_tps,omitempty"`
	GenerationThroughputTPS   *float64 `json:"generation_throughput_tps,omitempty"`
	KVCacheUtilizationAvgPct  *float64 `json:"kv_cache_utilization_avg_pct,omitempty"`
	KVCacheUtilizationPeakPct *float64 `json:"kv_cache_utilization_peak_pct,omitempty"`
	PrefixCacheHitRate        *float64 `json:"prefix_cache_hit_rate,omitempty"`
	PreemptionCount           *int     `json:"preemption_count,omitempty"`
	RunningRequestsAvg        *float64 `json:"running_requests_avg,omitempty"`
	RunningRequestsMax        *int     `json:"running_requests_max,omitempty"`
	OutputLengthMean          *float64 `json:"output_length_mean,omitempty"`

	// PRD-22: DCP GPU metrics from DCGM profiling counters.
	SMActiveAvgPct      *float64 `json:"sm_active_avg_pct,omitempty"`
	SMActivePeakPct     *float64 `json:"sm_active_peak_pct,omitempty"`
	TensorActiveAvgPct  *float64 `json:"tensor_active_avg_pct,omitempty"`
	TensorActivePeakPct *float64 `json:"tensor_active_peak_pct,omitempty"`
	DRAMActiveAvgPct    *float64 `json:"dram_active_avg_pct,omitempty"`
	DRAMActivePeakPct   *float64 `json:"dram_active_peak_pct,omitempty"`
	// Average framebuffer usage across scrapes (GiB).
	AcceleratorMemoryAvgGiB *float64 `json:"accelerator_memory_avg_gib,omitempty"`

	// PRD-59: per-node/per-role GPU breakdown for distributed runs. Nil/empty
	// for single-instance runs. Populated by the orchestrator from the keyed
	// scraper; PersistMetrics writes these into benchmark_metrics_by_shard in
	// the same transaction. Not a column on benchmark_metrics — a child table.
	Shards []ShardMetric `json:"shards,omitempty"`

	// PRD-62: disaggregation / KV-transfer / EPP-routing run-level summaries.
	// Non-null ONLY for disaggregated runs where the series populated (NIXL >=
	// 0.7.1 for kv_transfer_*, a reachable EPP for disagg_*). NULL on
	// single-instance, co-located, and historical rows.
	KVTransferTimeAvgMs        *float64 `json:"kv_transfer_time_avg_ms,omitempty"`
	KVTransferBytesTotal       *float64 `json:"kv_transfer_bytes_total,omitempty"`
	KVTransferFailures         *float64 `json:"kv_transfer_failures,omitempty"`
	PrefillTimeServerAvgMs     *float64 `json:"prefill_time_server_avg_ms,omitempty"`
	DecodeTimeServerAvgMs      *float64 `json:"decode_time_server_avg_ms,omitempty"`
	ExternalPrefixCacheHitRate *float64 `json:"external_prefix_cache_hit_rate,omitempty"`
	DisaggPrefillDecodeCount   *float64 `json:"disagg_prefill_decode_count,omitempty"`
	DisaggDecodeOnlyCount      *float64 `json:"disagg_decode_only_count,omitempty"`
	DisaggEngagedRatePct       *float64 `json:"disagg_engaged_rate_pct,omitempty"`
	PoolKVCacheUtilPct         *float64 `json:"pool_kv_cache_util_pct,omitempty"`
	PoolQueueSizeAvg           *float64 `json:"pool_queue_size_avg,omitempty"`
}

// ShardMetric is one serving shard's ({node, role}) GPU telemetry for a
// distributed run (PRD-59, table benchmark_metrics_by_shard). Only written for
// distributed/disaggregated runs; empty for single-instance runs.
type ShardMetric struct {
	RunID               string   `json:"run_id"`
	Node                string   `json:"node"`
	Role                string   `json:"role,omitempty"`
	Samples             int      `json:"samples"`
	UtilizationAvgPct   *float64 `json:"utilization_avg_pct,omitempty"`
	UtilizationPeakPct  *float64 `json:"utilization_peak_pct,omitempty"`
	MemoryAvgGiB        *float64 `json:"memory_avg_gib,omitempty"`
	MemoryPeakGiB       *float64 `json:"memory_peak_gib,omitempty"`
	SMActiveAvgPct      *float64 `json:"sm_active_avg_pct,omitempty"`
	SMActivePeakPct     *float64 `json:"sm_active_peak_pct,omitempty"`
	TensorActiveAvgPct  *float64 `json:"tensor_active_avg_pct,omitempty"`
	TensorActivePeakPct *float64 `json:"tensor_active_peak_pct,omitempty"`
	DRAMActiveAvgPct    *float64 `json:"dram_active_avg_pct,omitempty"`
	DRAMActivePeakPct   *float64 `json:"dram_active_peak_pct,omitempty"`
}

type Pricing struct {
	ID                    string   `json:"id"`
	InstanceTypeID        string   `json:"instance_type_id"`
	Region                string   `json:"region"`
	OnDemandHourlyUSD     float64  `json:"on_demand_hourly_usd"`
	Reserved1YrHourlyUSD  *float64 `json:"reserved_1yr_hourly_usd,omitempty"`
	Reserved3YrHourlyUSD  *float64 `json:"reserved_3yr_hourly_usd,omitempty"`
	EffectiveDate         string   `json:"effective_date"`
	CreatedAt             time.Time `json:"created_at"`
}

// RunRequest represents the input parameters for starting a benchmark run.
type RunRequest struct {
	ModelHfID            string  `json:"model_hf_id"`
	ModelHfRevision      string  `json:"model_hf_revision"`
	InstanceTypeName     string  `json:"instance_type_name"`
	Framework            string  `json:"framework"`
	FrameworkVersion     string  `json:"framework_version"`
	TensorParallelDegree int     `json:"tensor_parallel_degree"`
	Quantization         *string `json:"quantization,omitempty"`
	Concurrency          int     `json:"concurrency"`
	InputSequenceLength  int     `json:"input_sequence_length"`
	OutputSequenceLength int     `json:"output_sequence_length"`
	DatasetName          string  `json:"dataset_name"`
	RunType              string  `json:"run_type"`
	MaxModelLen          int     `json:"max_model_len,omitempty"`
	// PRD-46: vLLM scheduler knobs. Recommender populates these; direct
	// API submitters may set them to override. Zero / empty means "use
	// the recommender's choice if available, else vLLM's default."
	MaxNumBatchedTokens  int     `json:"max_num_batched_tokens,omitempty"`
	KVCacheDtype         string  `json:"kv_cache_dtype,omitempty"`
	// SGLang scheduler knobs. Zero means "use SGLang default".
	ChunkedPrefillSize   int     `json:"chunked_prefill_size,omitempty"`
	MemFractionStatic    float64 `json:"mem_fraction_static,omitempty"`
	// PRD-50: Run:ai streamer knobs. Empty string / 0 means "use default".
	StreamerMode           string `json:"streamer_mode,omitempty"`             // "" | "auto" | "off"
	StreamerConcurrency    int    `json:"streamer_concurrency,omitempty"`      // 0 → 16
	StreamerMemoryLimitGiB int    `json:"streamer_memory_limit_gib,omitempty"` // 0 → auto-sized
	ScenarioID           string  `json:"scenario_id,omitempty"` // scenario identifier (chatbot, batch, etc.)
	APIType              string  `json:"api_type,omitempty"`    // "chat_completion" (default) or "completion"
	ModelS3URI           string  `json:"model_s3_uri,omitempty"` // s3://bucket/path — load from S3 via Run:ai streamer
	HfToken              string  `json:"hf_token,omitempty"`
	// PRD-47 PR #6: when true, the host-memory feasibility check is
	// downgraded to a warning for this run. Useful when the operator
	// has verified empirically that a rejected model fits on the host
	// (or there's just no history yet to calibrate against). Does not
	// override GPU-memory, TP, or pipeline-tag checks — those are
	// architectural, not statistical.
	AllowHostMemOverride bool `json:"allow_host_mem_override,omitempty"`

	// PRD-56/57: distributed (multi-node) topology.
	//   DeploymentMode "" / "single" (default) ⇒ single-instance path,
	//     unchanged. "distributed" ⇒ multi-node llm-d run.
	//   NodeCount / PipelineParallelDegree / NetworkMode are PERSISTED
	//     (PRD-57, migration 035); TP reuses the existing
	//     TensorParallelDegree field (within-node TP for distributed runs).
	//   GPUsPerNode / NodePoolOverride stay TRANSIENT — threaded into
	//     RunConfig but not written to benchmark_runs (GPUsPerNode defaults
	//     to the instance's accelerator count; NodePoolOverride is an
	//     operational knob, not a run property).
	DeploymentMode         string `json:"deployment_mode,omitempty"` // "" | "single" | "distributed" | "disaggregated"
	NodeCount              int    `json:"node_count,omitempty"`
	PipelineParallelDegree int    `json:"pipeline_parallel_degree,omitempty"`
	GPUsPerNode            int    `json:"gpus_per_node,omitempty"`
	NetworkMode            string `json:"network_mode,omitempty"`       // "efa" (default) | "tcp"
	NodePoolOverride       string `json:"node_pool_override,omitempty"` // pin a specific multinode-<az>/test pool

	// PRD-58: prefill/decode disaggregation. Consulted only when
	// DeploymentMode == "disaggregated". Per-role replica count + parallelism
	// (TP within-node, PP across-node — independent knobs, TP=1 allowed).
	// PERSISTED (migration 036). The KV connector/backend are derived from
	// NetworkMode by the orchestrator (nixl + tcp|libfabric), not user-set.
	PrefillReplicas int `json:"prefill_replicas,omitempty"`
	PrefillTP       int `json:"prefill_tp,omitempty"`
	PrefillPP       int `json:"prefill_pp,omitempty"`
	DecodeReplicas  int `json:"decode_replicas,omitempty"`
	DecodeTP        int `json:"decode_tp,omitempty"`
	DecodePP        int `json:"decode_pp,omitempty"`
	// PRD-63: optional co-located "both" pool. 0 ⇒ no both pool (today's PD
	// behavior). both_pp intentionally omitted (per-role PP > 1 is a non-goal).
	BothReplicas int `json:"both_replicas,omitempty"`
	BothTP       int `json:"both_tp,omitempty"`
	// PRD-64: optional per-role scheduler override (D/P only). 0 ⇒ inherit the
	// shared MaxNumBatchedTokens. PRD-63 adds the symmetric "both" knob.
	PrefillMaxNumBatchedTokens int `json:"prefill_max_num_batched_tokens,omitempty"`
	DecodeMaxNumBatchedTokens  int `json:"decode_max_num_batched_tokens,omitempty"`
	BothMaxNumBatchedTokens    int `json:"both_max_num_batched_tokens,omitempty"`
	// PRD-61: run-tunable EPP routing config (disaggregated only). All optional.
	// PDNonCachedTokens is a POINTER because 0 is meaningful (disable PD) and must
	// be distinguishable from "omitted → default 16"; the others use 0 = omitted
	// (a 0 weight/size is never valid). PDDeciderStrategy "" = omitted → threshold.
	PDNonCachedTokens      *int   `json:"pd_noncached_tokens,omitempty"`
	PDPrefixCacheWeight    int    `json:"pd_prefix_cache_weight,omitempty"`
	PDQueueScorerWeight    int    `json:"pd_queue_scorer_weight,omitempty"`
	PDMaxPrefixBlocks      int    `json:"pd_max_prefix_blocks,omitempty"`
	PDLRUCapacityPerServer int    `json:"pd_lru_capacity_per_server,omitempty"`
	PDDeciderStrategy      string `json:"pd_decider_strategy,omitempty"`
}

// TestSuiteRun represents a test suite execution.
type TestSuiteRun struct {
	ID                   string     `json:"id"`
	ModelID              string     `json:"model_id"`
	InstanceTypeID       string     `json:"instance_type_id"`
	SuiteID              string     `json:"suite_id"`
	TensorParallelDegree int        `json:"tensor_parallel_degree"`
	Quantization         *string    `json:"quantization,omitempty"`
	MaxModelLen          int        `json:"max_model_len,omitempty"`
	// PRD-46: vLLM scheduler knobs (see BenchmarkRun).
	MaxNumBatchedTokens *int    `json:"max_num_batched_tokens,omitempty"`
	KVCacheDtype        *string `json:"kv_cache_dtype,omitempty"`
	// SGLang scheduler knobs (see BenchmarkRun).
	ChunkedPrefillSize *int     `json:"chunked_prefill_size,omitempty"`
	MemFractionStatic  *float64 `json:"mem_fraction_static,omitempty"`
	// PRD-50: Run:ai streamer knobs (see BenchmarkRun).
	StreamerMode           *string `json:"streamer_mode,omitempty"`
	StreamerConcurrency    *int    `json:"streamer_concurrency,omitempty"`
	StreamerMemoryLimitGiB *int    `json:"streamer_memory_limit_gib,omitempty"`
	Status               string     `json:"status"`
	CurrentScenario      *string    `json:"current_scenario,omitempty"`
	StartedAt            *time.Time `json:"started_at,omitempty"`
	CompletedAt          *time.Time `json:"completed_at,omitempty"`
	CreatedAt            time.Time  `json:"created_at"`
	// PRD-41: fields needed to reconstruct the deployment manifest for
	// suite export. Nullable for historical rows created before migration 026.
	Framework        *string `json:"framework,omitempty"`
	FrameworkVersion *string `json:"framework_version,omitempty"`
	ModelS3URI       *string `json:"model_s3_uri,omitempty"`
	// PRD-35: SUM of child benchmark_runs.total_cost_usd, written once when
	// the suite marks itself completed. NULL if every child is NULL.
	TotalCostUSD *float64 `json:"total_cost_usd,omitempty"`
	// PRD-40: replica coordination (see BenchmarkRun).
	OwnerPod        *string `json:"owner_pod,omitempty"`
	CancelRequested bool    `json:"cancel_requested"`
	// PRD-47: peak container workingSetBytes observed during the shared
	// model-load phase, in GiB. Suite-level because one model deployment
	// is reused across all scenarios.
	HostMemoryPeakGiB *float64 `json:"host_memory_peak_gib,omitempty"`
}

// ScenarioResult represents the result of a single scenario within a suite run.
type ScenarioResult struct {
	ID                string     `json:"id"`
	SuiteRunID        string     `json:"suite_run_id"`
	ScenarioID        string     `json:"scenario_id"`
	Status            string     `json:"status"`
	ErrorMessage      *string    `json:"error_message,omitempty"`
	StartedAt         *time.Time `json:"started_at,omitempty"`
	CompletedAt       *time.Time `json:"completed_at,omitempty"`
	TTFTP50Ms         *float64   `json:"ttft_p50_ms,omitempty"`
	TTFTP90Ms         *float64   `json:"ttft_p90_ms,omitempty"`
	TTFTP95Ms         *float64   `json:"ttft_p95_ms,omitempty"`
	TTFTP99Ms         *float64   `json:"ttft_p99_ms,omitempty"`
	E2ELatencyP50Ms   *float64   `json:"e2e_latency_p50_ms,omitempty"`
	E2ELatencyP90Ms   *float64   `json:"e2e_latency_p90_ms,omitempty"`
	E2ELatencyP95Ms   *float64   `json:"e2e_latency_p95_ms,omitempty"`
	E2ELatencyP99Ms   *float64   `json:"e2e_latency_p99_ms,omitempty"`
	ITLP50Ms          *float64   `json:"itl_p50_ms,omitempty"`
	ITLP90Ms          *float64   `json:"itl_p90_ms,omitempty"`
	ITLP95Ms          *float64   `json:"itl_p95_ms,omitempty"`
	ITLP99Ms          *float64   `json:"itl_p99_ms,omitempty"`
	TPOTP50Ms                *float64   `json:"tpot_p50_ms,omitempty"`
	TPOTP90Ms                *float64   `json:"tpot_p90_ms,omitempty"`
	TPOTP99Ms                *float64   `json:"tpot_p99_ms,omitempty"`
	ThroughputTPS            *float64   `json:"throughput_tps,omitempty"`
	RequestsPerSecond        *float64   `json:"requests_per_second,omitempty"`
	SuccessfulRequests       *int       `json:"successful_requests,omitempty"`
	FailedRequests           *int       `json:"failed_requests,omitempty"`
	WaitingRequestsMax       *int       `json:"waiting_requests_max,omitempty"`
	AcceleratorUtilizationPct    *float64 `json:"accelerator_utilization_pct,omitempty"`
	AcceleratorUtilizationAvgPct *float64 `json:"accelerator_utilization_avg_pct,omitempty"`
	AcceleratorMemoryPeakGiB     *float64 `json:"accelerator_memory_peak_gib,omitempty"`
	// PRD-22: DCP metrics
	SMActiveAvgPct      *float64 `json:"sm_active_avg_pct,omitempty"`
	SMActivePeakPct     *float64 `json:"sm_active_peak_pct,omitempty"`
	TensorActiveAvgPct  *float64 `json:"tensor_active_avg_pct,omitempty"`
	TensorActivePeakPct *float64 `json:"tensor_active_peak_pct,omitempty"`
	DRAMActiveAvgPct    *float64 `json:"dram_active_avg_pct,omitempty"`
	DRAMActivePeakPct   *float64 `json:"dram_active_peak_pct,omitempty"`
	// Average framebuffer usage across scrapes (GiB).
	AcceleratorMemoryAvgGiB *float64 `json:"accelerator_memory_avg_gib,omitempty"`
	LoadgenConfig           *string    `json:"loadgen_config,omitempty"`
	CreatedAt               time.Time  `json:"created_at"`
}

// SuiteRunRequest represents the input parameters for starting a test suite run.
type SuiteRunRequest struct {
	ModelHfID            string   `json:"model_hf_id"`
	ModelHfRevision      string   `json:"model_hf_revision"`
	InstanceTypeName     string   `json:"instance_type_name"`
	SuiteID              string   `json:"suite_id,omitempty"`     // Predefined suite ID
	ScenarioIDs          []string `json:"scenario_ids,omitempty"` // Custom scenario list (alternative to suite_id)
	Framework            string   `json:"framework"`
	FrameworkVersion     string   `json:"framework_version"`
	TensorParallelDegree int      `json:"tensor_parallel_degree"`
	Quantization         *string  `json:"quantization,omitempty"`
	MaxModelLen          int      `json:"max_model_len,omitempty"`
	// PRD-46: vLLM scheduler knobs (see RunRequest).
	MaxNumBatchedTokens  int      `json:"max_num_batched_tokens,omitempty"`
	KVCacheDtype         string   `json:"kv_cache_dtype,omitempty"`
	// SGLang scheduler knobs (see RunRequest).
	ChunkedPrefillSize   int     `json:"chunked_prefill_size,omitempty"`
	MemFractionStatic    float64 `json:"mem_fraction_static,omitempty"`
	// PRD-50: Run:ai streamer knobs (see RunRequest).
	StreamerMode           string `json:"streamer_mode,omitempty"`
	StreamerConcurrency    int    `json:"streamer_concurrency,omitempty"`
	StreamerMemoryLimitGiB int    `json:"streamer_memory_limit_gib,omitempty"`
	ModelS3URI           string   `json:"model_s3_uri,omitempty"` // s3://bucket/path — load from S3 via Run:ai streamer
	HfToken              string   `json:"hf_token,omitempty"`
	// PRD-47 PR #6: see RunRequest.
	AllowHostMemOverride bool `json:"allow_host_mem_override,omitempty"`
}

// ModelCache tracks models cached from HuggingFace to S3, or custom S3 models.
type ModelCache struct {
	ID           string     `json:"id"`
	HfID         *string    `json:"hf_id,omitempty"`
	HfRevision   string     `json:"hf_revision"`
	S3URI        string     `json:"s3_uri"`
	DisplayName  string     `json:"display_name"`
	SizeBytes    *int64     `json:"size_bytes,omitempty"`
	Status       string     `json:"status"`
	ErrorMessage *string    `json:"error_message,omitempty"`
	JobName      *string    `json:"job_name,omitempty"`
	CachedAt     *time.Time `json:"cached_at,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
}

type CacheModelRequest struct {
	ModelHfID  string `json:"model_hf_id"`
	HfRevision string `json:"hf_revision,omitempty"`
	HfToken    string `json:"hf_token,omitempty"`
}

// RegisterCustomModelRequest registers an existing model directory in S3 as
// a cached entry without running a HF download. The caller must already have
// uploaded an HF-snapshot-layout directory to the given S3 URI.
type RegisterCustomModelRequest struct {
	S3URI       string `json:"s3_uri"`
	DisplayName string `json:"display_name"`
}

// CatalogModel is a model in the seeding matrix (PRD-30).
type CatalogModel struct {
	ID        int       `json:"id"`
	HfID      string    `json:"hf_id"`
	Family    *string   `json:"family,omitempty"`
	Enabled   bool      `json:"enabled"`
	UpdatedAt time.Time `json:"updated_at"`
}

// CatalogInstanceType is an instance type in the seeding matrix.
type CatalogInstanceType struct {
	ID        int       `json:"id"`
	Name      string    `json:"name"`
	Enabled   bool      `json:"enabled"`
	UpdatedAt time.Time `json:"updated_at"`
}

// CatalogSeedDefaults is the singleton row of per-seed-run defaults.
type CatalogSeedDefaults struct {
	Scenario  string    `json:"scenario"`
	Dataset   string    `json:"dataset"`
	UpdatedAt time.Time `json:"updated_at"`
}

// ToolVersions is the singleton row holding platform-wide tool versions
// (PRD-34). framework_version is the vLLM image tag; sglang_version is
// the SGLang image tag (used when a run's framework="sglang");
// inference_perf_version is the inference-perf image tag.
type ToolVersions struct {
	FrameworkVersion     string    `json:"framework_version"`
	SGLangVersion        string    `json:"sglang_version"`
	InferencePerfVersion string    `json:"inference_perf_version"`
	// PRD-66 Part 2: settable tags for the two multi-node images.
	// LLMDVersion is the co-located PP image (llm-d-aws) tag; PDVLLMVersion is
	// the disaggregated D/P image (vllm/vllm-openai) tag — DISTINCT from
	// FrameworkVersion because D/P pins a cu13/NIXL-specific vLLM.
	LLMDVersion   string    `json:"llmd_version"`
	PDVLLMVersion string    `json:"pd_vllm_version"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// CatalogMatrix bundles the full matrix for the seeder.
type CatalogMatrix struct {
	Defaults      CatalogSeedDefaults   `json:"defaults"`
	Models        []CatalogModel        `json:"models"`
	InstanceTypes []CatalogInstanceType `json:"instance_types"`
}

// CatalogSeedStatus tracks an in-process seed run (PRD-30).
type CatalogSeedStatus struct {
	ID           string     `json:"id"`
	Status       string     `json:"status"` // active | completed | failed | interrupted
	Total        int        `json:"total"`
	Completed    int        `json:"completed"`
	DryRun       bool       `json:"dry_run"`
	ErrorMessage *string    `json:"error_message,omitempty"`
	StartedAt    time.Time  `json:"started_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
	CompletedAt  *time.Time `json:"completed_at,omitempty"`
	// PRD-40: API pod running the seed. Used by ownership-aware orphan
	// recovery so a restarted sibling pod doesn't wipe a live seed.
	OwnerPod *string `json:"owner_pod,omitempty"`
}

// RunKey is a (model_hf_id, instance_type_name) dedup key for the seeder.
type RunKey struct {
	ModelHfID        string
	InstanceTypeName string
}

// ScenarioOverride is a per-scenario partial override of the code-defined
// inference-perf knobs (PRD-32). All non-ID fields are pointers so NULL in
// SQL means "inherit from the code-defined scenario."
type ScenarioOverride struct {
	ScenarioID string    `json:"scenario_id"`
	NumWorkers *int      `json:"num_workers,omitempty"`
	Streaming  *bool     `json:"streaming,omitempty"`
	InputMean  *int      `json:"input_mean,omitempty"`
	OutputMean *int      `json:"output_mean,omitempty"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// ConfigAuditEntry is a single row in config_audit_log (PRD-32).
type ConfigAuditEntry struct {
	ID      int64     `json:"id"`
	At      time.Time `json:"at"`
	Action  string    `json:"action"`
	Actor   *string   `json:"actor,omitempty"`
	Summary string    `json:"summary"`
}
