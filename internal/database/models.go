package database

import (
	"time"
)

type Model struct {
	ID             string    `json:"id"`
	HfID           string    `json:"hf_id"`
	HfRevision     string    `json:"hf_revision"`
	ModelFamily    *string   `json:"model_family,omitempty"`
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
	Quantization          *string    `json:"quantization,omitempty"`
	Concurrency           int        `json:"concurrency"`
	InputSequenceLength   int        `json:"input_sequence_length"`
	OutputSequenceLength  int        `json:"output_sequence_length"`
	DatasetName           string     `json:"dataset_name"`
	RunType               string     `json:"run_type"`
	MinDurationSeconds    int        `json:"min_duration_seconds"`
	MaxModelLen           int        `json:"max_model_len,omitempty"`
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
	MinDurationSeconds   int     `json:"min_duration_seconds,omitempty"`
	ScenarioID           string  `json:"scenario_id,omitempty"` // scenario identifier (chatbot, batch, etc.)
	APIType              string  `json:"api_type,omitempty"`    // "chat_completion" (default) or "completion"
	ModelS3URI           string  `json:"model_s3_uri,omitempty"` // s3://bucket/path — load from S3 via Run:ai streamer
	HfToken              string  `json:"hf_token,omitempty"`
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
	Status               string     `json:"status"`
	CurrentScenario      *string    `json:"current_scenario,omitempty"`
	StartedAt            *time.Time `json:"started_at,omitempty"`
	CompletedAt          *time.Time `json:"completed_at,omitempty"`
	CreatedAt            time.Time  `json:"created_at"`
	// PRD-35: SUM of child benchmark_runs.total_cost_usd, written once when
	// the suite marks itself completed. NULL if every child is NULL.
	TotalCostUSD *float64 `json:"total_cost_usd,omitempty"`
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
	ModelS3URI           string   `json:"model_s3_uri,omitempty"` // s3://bucket/path — load from S3 via Run:ai streamer
	HfToken              string   `json:"hf_token,omitempty"`
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
	Scenario           string    `json:"scenario"`
	Dataset            string    `json:"dataset"`
	MinDurationSeconds int       `json:"min_duration_seconds"`
	UpdatedAt          time.Time `json:"updated_at"`
}

// ToolVersions is the singleton row holding platform-wide tool versions
// (PRD-34). framework_version is the vLLM image tag; inference_perf_version
// is the inference-perf image tag. Both apply to every new benchmark run.
type ToolVersions struct {
	FrameworkVersion     string    `json:"framework_version"`
	InferencePerfVersion string    `json:"inference_perf_version"`
	UpdatedAt            time.Time `json:"updated_at"`
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
