export interface CatalogEntry {
  run_id: string;
  model_hf_id: string;
  model_family?: string;
  parameter_count?: number;
  instance_type_name: string;
  instance_family: string;
  accelerator_type: string;
  accelerator_name: string;
  accelerator_count: number;
  accelerator_memory_gib: number;
  framework: string;
  framework_version: string;
  tensor_parallel_degree: number;
  quantization?: string;
  concurrency: number;
  input_sequence_length: number;
  output_sequence_length: number;
  completed_at?: string;
  ttft_p50_ms?: number;
  ttft_p99_ms?: number;
  e2e_latency_p50_ms?: number;
  e2e_latency_p99_ms?: number;
  itl_p50_ms?: number;
  itl_p99_ms?: number;
  throughput_per_request_tps?: number;
  throughput_aggregate_tps?: number;
  requests_per_second?: number;
  accelerator_utilization_pct?: number;
  accelerator_memory_peak_gib?: number;
}

export interface BenchmarkRun {
  id: string;
  model_id: string;
  instance_type_id: string;
  framework: string;
  framework_version: string;
  tensor_parallel_degree: number;
  quantization?: string;
  concurrency: number;
  input_sequence_length: number;
  output_sequence_length: number;
  dataset_name: string;
  run_type: string;
  status: string;
  superseded: boolean;
  started_at?: string;
  completed_at?: string;
  created_at: string;
}

export interface BenchmarkMetrics {
  id: string;
  run_id: string;
  ttft_p50_ms?: number;
  ttft_p90_ms?: number;
  ttft_p95_ms?: number;
  ttft_p99_ms?: number;
  e2e_latency_p50_ms?: number;
  e2e_latency_p90_ms?: number;
  e2e_latency_p95_ms?: number;
  e2e_latency_p99_ms?: number;
  itl_p50_ms?: number;
  itl_p90_ms?: number;
  itl_p95_ms?: number;
  itl_p99_ms?: number;
  throughput_per_request_tps?: number;
  throughput_aggregate_tps?: number;
  requests_per_second?: number;
  accelerator_utilization_pct?: number;
  accelerator_utilization_avg_pct?: number;
  accelerator_memory_peak_gib?: number;
  waiting_requests_max?: number;
  successful_requests?: number;
  failed_requests?: number;
  total_duration_seconds?: number;
  // Extended metrics (PRD-14)
  tpot_p50_ms?: number;
  tpot_p90_ms?: number;
  tpot_p99_ms?: number;
  prefill_time_p50_ms?: number;
  decode_time_p50_ms?: number;
  queue_time_p50_ms?: number;
  prompt_throughput_tps?: number;
  generation_throughput_tps?: number;
  kv_cache_utilization_avg_pct?: number;
  kv_cache_utilization_peak_pct?: number;
  prefix_cache_hit_rate?: number;
  preemption_count?: number;
  running_requests_avg?: number;
  running_requests_max?: number;
  output_length_mean?: number;
}

export interface RunRequest {
  model_hf_id: string;
  model_hf_revision: string;
  instance_type_name: string;
  framework: string;
  framework_version: string;
  tensor_parallel_degree: number;
  quantization?: string;
  concurrency: number;
  input_sequence_length: number;
  output_sequence_length: number;
  dataset_name?: string;
  run_type?: string;
  scenario_id?: string;
  max_model_len?: number;
  min_duration_seconds?: number;
  hf_token?: string;
}

export interface RunListItem {
  id: string;
  model_hf_id: string;
  instance_type_name: string;
  framework: string;
  run_type: string;
  status: string;
  created_at: string;
  started_at?: string;
  completed_at?: string;
}

export interface RunListFilter {
  status?: string;
  model?: string;
  limit?: number;
  offset?: number;
}

export type PricingTier = "on_demand" | "reserved_1yr" | "reserved_3yr";

export interface PricingRow {
  instance_type_name: string;
  on_demand_hourly_usd: number;
  reserved_1yr_hourly_usd?: number;
  reserved_3yr_hourly_usd?: number;
  effective_date: string;
}

export interface RecommendExplanation {
  tensor_parallel_degree: string;
  quantization: string;
  max_model_len: string;
  concurrency: string;
  feasible: boolean;
  reason?: string;
  suggested_instance?: string;
}

export interface RecommendModelInfo {
  parameter_count: number;
  native_dtype: string;
  max_position_embeddings: number;
  architecture: string;
  sliding_window?: number; // 0 or undefined = full attention
}

export interface RecommendInstanceInfo {
  accelerator_count: number;
  accelerator_memory_gib: number;
  accelerator_name: string;
}

export interface QuantizationOption {
  quantization: string;
  estimated_mem_gib: number;
}

export interface RecommendAlternatives {
  quantization_option?: QuantizationOption;
  larger_instance?: string;
}

export interface RecommendResponse {
  tensor_parallel_degree: number;
  quantization?: string | null;
  max_model_len: number;
  concurrency: number;
  input_sequence_length: number;
  output_sequence_length: number;
  overhead_gib: number;
  explanation: RecommendExplanation;
  model_info: RecommendModelInfo;
  instance_info: RecommendInstanceInfo;
  alternatives?: RecommendAlternatives;
  valid_tp_options?: number[];
}

export interface InstanceType {
  id: string;
  name: string;
  family: string;
  accelerator_type: string;
  accelerator_name: string;
  accelerator_count: number;
  accelerator_memory_gib: number;
  vcpus: number;
  memory_gib: number;
}

export interface CatalogSeedStatus {
  job_name?: string;
  status: "none" | "active" | "succeeded" | "failed";
  started_at?: string;
  completed_at?: string;
}

export interface CatalogFilter {
  model?: string;
  model_family?: string;
  instance_family?: string;
  accelerator_type?: string;
  sort?: string;
  order?: "asc" | "desc";
  limit?: number;
  offset?: number;
}

// Estimate types
export interface EstimateFilter {
  accelerator_type?: string;
  max_cost_hourly?: number;
  min_context_length?: number;
  quantization?: string;
  region?: string;
}

export interface EstimateConfig {
  tensor_parallel_degree: number;
  quantization?: string | null;
  max_model_len: number;
  concurrency: number;
  input_sequence_length: number;
  output_sequence_length: number;
}

export interface MemoryEstimate {
  model_weights_gib: number;
  available_gib: number;
  utilization_pct: number;
}

export interface CostEstimate {
  hourly_usd: number;
}

export interface EstimateRow {
  instance_type: string;
  accelerator_type: string;
  accelerator_name: string;
  accelerator_count: number;
  feasible: boolean;
  requires_quantization: boolean;
  config?: EstimateConfig;
  memory?: MemoryEstimate;
  cost?: CostEstimate;
  explanation?: string;
  has_benchmark_data: boolean;
}

export interface EstimateModelInfo {
  hf_id: string;
  parameter_count: number;
  native_dtype: string;
  max_position_embeddings: number;
  architecture: string;
  num_attention_heads: number;
  num_kv_heads: number;
}

export interface EstimateSummary {
  total_evaluated: number;
  feasible_native: number;
  feasible_quantized: number;
  infeasible: number;
  cheapest_feasible?: string;
  most_headroom?: string;
}

export interface EstimateResponse {
  model_info: EstimateModelInfo;
  estimates: EstimateRow[];
  summary: EstimateSummary;
}

// PRD-15: Memory breakdown types
export interface MemoryBreakdown {
  model_weights_gib: number;
  kv_cache_gib: number;
  quantization_metadata_gib: number;
  block_table_gib: number;
  runtime_overhead_gib: number;
  total_used_gib: number;
  total_available_gib: number;
  headroom_gib: number;
}

export interface MemoryBreakdownResponse extends MemoryBreakdown {
  warning_message?: string;
}

// PRD-15: OOM history types
export interface OOMEvent {
  id: string;
  run_id?: string;
  model_hf_id: string;
  instance_type: string;
  pod_name: string;
  container_name?: string;
  detection_method: string;
  exit_code?: number;
  message: string;
  occurred_at: string;
  created_at: string;
  tensor_parallel_degree?: number;
  concurrency?: number;
  max_model_len?: number;
  quantization?: string;
}

export interface OOMHistory {
  model_hf_id: string;
  instance_type: string;
  events: OOMEvent[];
  total_count: number;
}

// PRD-12: Scenarios
export interface LoadStage {
  duration: number;
  rate: number;
}

export interface Scenario {
  id: string;
  name: string;
  description: string;
  duration_seconds: number;
  load_type: string;
  stages: LoadStage[];
}

// PRD-13: Test Suites
export interface TestSuite {
  id: string;
  name: string;
  description: string;
  scenarios: string[];
  total_duration_seconds: number;
}

export interface SuiteRunRequest {
  model_hf_id: string;
  model_hf_revision?: string;
  instance_type_name: string;
  suite_id?: string;           // Predefined suite ID
  scenario_ids?: string[];     // Custom scenario list (alternative to suite_id)
  framework?: string;
  framework_version?: string;
  tensor_parallel_degree?: number;
  quantization?: string;
  max_model_len?: number;
  hf_token?: string;
}

export interface ScenarioProgress {
  id: string;
  status: string;
}

export interface SuiteRunProgress {
  completed: number;
  total: number;
  scenarios: ScenarioProgress[];
}

export interface ScenarioResult {
  id: string;
  suite_run_id: string;
  scenario_id: string;
  status: string;
  error_message?: string;
  started_at?: string;
  completed_at?: string;
  // Metrics (populated when status === "completed")
  ttft_p50_ms?: number;
  ttft_p90_ms?: number;
  ttft_p99_ms?: number;
  e2e_latency_p50_ms?: number;
  e2e_latency_p90_ms?: number;
  e2e_latency_p99_ms?: number;
  itl_p50_ms?: number;
  itl_p90_ms?: number;
  itl_p99_ms?: number;
  throughput_tps?: number;
  requests_per_second?: number;
  successful_requests?: number;
  failed_requests?: number;
  accelerator_utilization_pct?: number;
  accelerator_memory_peak_gib?: number;
}

export interface ScenarioDefinition {
  id: string;
  name: string;
  target_qps: number;
  duration_seconds: number;
  load_type: string;
}

export interface TestSuiteRun {
  id: string;
  model_id: string;
  instance_type_id: string;
  suite_id: string;
  status: string;
  started_at?: string;
  completed_at?: string;
  created_at: string;
  progress?: SuiteRunProgress;
  results?: ScenarioResult[];
  scenario_definitions?: ScenarioDefinition[];
}
