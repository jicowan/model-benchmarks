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
	Status                string     `json:"status"`
	Superseded            bool       `json:"superseded"`
	StartedAt             *time.Time `json:"started_at,omitempty"`
	CompletedAt           *time.Time `json:"completed_at,omitempty"`
	CreatedAt             time.Time  `json:"created_at"`
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
	AcceleratorUtilizationPct *float64 `json:"accelerator_utilization_pct,omitempty"`
	AcceleratorMemoryPeakGiB *float64 `json:"accelerator_memory_peak_gib,omitempty"`
	SuccessfulRequests       *int     `json:"successful_requests,omitempty"`
	FailedRequests           *int     `json:"failed_requests,omitempty"`
	TotalDurationSeconds     *float64 `json:"total_duration_seconds,omitempty"`
	CreatedAt                time.Time `json:"created_at"`
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
	HfToken              string  `json:"hf_token,omitempty"`
}
