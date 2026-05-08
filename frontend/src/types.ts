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
  ttft_p95_ms?: number;
  ttft_p99_ms?: number;
  e2e_latency_p50_ms?: number;
  e2e_latency_p95_ms?: number;
  e2e_latency_p99_ms?: number;
  itl_p50_ms?: number;
  itl_p95_ms?: number;
  itl_p99_ms?: number;
  throughput_per_request_tps?: number;
  throughput_aggregate_tps?: number;
  requests_per_second?: number;
  successful_requests?: number;
  failed_requests?: number;
  accelerator_utilization_pct?: number;
  accelerator_utilization_avg_pct?: number;
  accelerator_memory_peak_gib?: number;
  accelerator_memory_avg_gib?: number;
  // PRD-22: DCP GPU metrics
  sm_active_avg_pct?: number;
  tensor_active_avg_pct?: number;
  dram_active_avg_pct?: number;
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
  // vLLM deployment configuration. These already flow on the JSON from
  // the run detail endpoint; surfaced on the TS type so the Configuration
  // panel can render every knob used at deploy time.
  max_model_len?: number;
  max_num_batched_tokens?: number | null;
  kv_cache_dtype?: string | null;
  scenario_id?: string | null;
  model_s3_uri?: string | null;
  status: string;
  error_message?: string;
  superseded: boolean;
  started_at?: string;
  completed_at?: string;
  created_at: string;
  // Enrichments from the runs API — populated for display.
  // model_hf_id will be the S3 URI for S3-only custom models (API stores the
  // URI in this field when no HF id is available) — the UI should render it
  // as-is regardless of origin.
  model_hf_id?: string;
  instance_type_name?: string;
  // PRD-35: cost persisted at run completion. Null on historical rows and
  // when pricing was unavailable — UI should omit the overline rather than
  // show $0.
  total_cost_usd?: number | null;
  loadgen_cost_usd?: number | null;
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
  // PRD-22: DCP GPU metrics
  sm_active_avg_pct?: number;
  sm_active_peak_pct?: number;
  tensor_active_avg_pct?: number;
  tensor_active_peak_pct?: number;
  dram_active_avg_pct?: number;
  dram_active_peak_pct?: number;
  // Average framebuffer usage (GiB) across scrapes.
  accelerator_memory_avg_gib?: number;
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
  max_num_batched_tokens?: number;
  kv_cache_dtype?: string;
  api_type?: string;
  model_s3_uri?: string;
  hf_token?: string;
}

export interface RunListItem {
  id: string;
  model_hf_id: string;
  instance_type_name: string;
  framework: string;
  run_type: string;
  status: string;
  error_message?: string;
  created_at: string;
  started_at?: string;
  completed_at?: string;
}

// PRD-36: unified row shape returned by GET /api/v1/jobs. The Runs page
// consumes these instead of merging single-runs + suite-runs client-side.
export interface Job {
  id: string;
  type: "run" | "suite";
  model_hf_id: string;
  instance_type_name: string;
  // For type="run" this is the vLLM framework string; for type="suite" it's
  // the suite_id (e.g. "quick", "regression").
  framework_or_suite: string;
  status: string;
  error_message?: string;
  created_at: string;
  started_at?: string;
  completed_at?: string;
}

export interface JobFilter {
  type?: "run" | "suite";
  status?: string;
  model?: string;
  sort?: string;
  order?: "asc" | "desc";
  limit?: number;
  offset?: number;
}

// Retained for backwards compatibility with any remaining callers; prefer
// JobFilter going forward.
export interface RunListFilter {
  status?: string;
  model?: string;
  sort?: string;
  order?: "asc" | "desc";
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
  production_note?: string;
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
  max_num_batched_tokens?: number;
  kv_cache_dtype?: string;
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
  seed_id?: string;
  status: "none" | "active" | "completed" | "failed" | "interrupted";
  total?: number;
  completed?: number;
  dry_run?: boolean;
  error_message?: string;
  started_at?: string;
  completed_at?: string;
}

export interface CatalogFilter {
  ids?: string[]; // PRD-36: used by Compare to fetch selected rows only
  model?: string;
  model_family?: string;
  instance_family?: string;
  accelerator_type?: string;
  sort?: string;
  order?: "asc" | "desc";
  limit?: number;
  offset?: number;
}

// PRD-36: ModelCache list filter for server-side pagination + sort.
export interface ModelCacheFilter {
  status?: string;
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
  max_num_batched_tokens?: number;
  kv_cache_dtype?: string;
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
  max_num_batched_tokens?: number;
  kv_cache_dtype?: string;
  model_s3_uri?: string;
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
  tpot_p50_ms?: number;
  tpot_p90_ms?: number;
  tpot_p99_ms?: number;
  throughput_tps?: number;
  requests_per_second?: number;
  successful_requests?: number;
  failed_requests?: number;
  waiting_requests_max?: number;
  accelerator_utilization_pct?: number;
  accelerator_utilization_avg_pct?: number;
  accelerator_memory_peak_gib?: number;
  accelerator_memory_avg_gib?: number;
  // PRD-22: DCP GPU metrics
  sm_active_avg_pct?: number;
  sm_active_peak_pct?: number;
  tensor_active_avg_pct?: number;
  tensor_active_peak_pct?: number;
  dram_active_avg_pct?: number;
  dram_active_peak_pct?: number;
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
  // Enrichments from the suite-run API
  model_hf_id?: string;
  instance_type_name?: string;
  accelerator_type?: string;
  accelerator_name?: string;
  accelerator_count?: number;
  accelerator_memory_gib?: number;
  // vLLM deployment configuration. These are set at suite creation time
  // and shared across every scenario in the suite (one model deployment
  // serving all scenarios). Surfaced in the Configuration panel on the
  // Suite result page so users see the knobs used to launch the model.
  tensor_parallel_degree?: number;
  quantization?: string | null;
  max_model_len?: number;
  max_num_batched_tokens?: number | null;
  max_num_seqs?: number;
  kv_cache_dtype?: string | null;
  framework?: string | null;
  framework_version?: string | null;
  model_s3_uri?: string | null;
  // PRD-35: cost frozen at suite completion (hourly × own started→completed
  // window; all scenarios share one EC2 node).
  total_cost_usd?: number | null;
}

export interface ComponentStatus {
  status: "ok" | "down";
  latency_ms?: number;
  error?: string;
}

export interface StatusResponse {
  status: "ok" | "degraded" | "down";
  components: Record<string, ComponentStatus>;
  checked_at: string;
}

// PRD-21: Model Cache
export interface ModelCache {
  id: string;
  hf_id?: string;
  hf_revision: string;
  s3_uri: string;
  display_name: string;
  size_bytes?: number;
  status: "pending" | "caching" | "cached" | "failed" | "deleting";
  error_message?: string;
  job_name?: string;
  cached_at?: string;
  created_at: string;
}

export interface CacheModelRequest {
  model_hf_id: string;
  hf_revision?: string;
  hf_token?: string;
}

export interface RegisterCustomModelRequest {
  s3_uri: string;
  display_name: string;
}

// PRD-31: credentials management
export interface CredentialMetadata {
  set: boolean;
  updated_at?: string;
}

export interface CredentialsStatus {
  hf_token: CredentialMetadata;
  dockerhub_token: CredentialMetadata;
}

// PRD-32: catalog matrix editor
export interface CatalogSeedDefaults {
  scenario: string;
  dataset: string;
  updated_at?: string;
}

// PRD-34: tool versions (vLLM + inference-perf) singleton.
export interface ToolVersions {
  framework_version: string;
  inference_perf_version: string;
  updated_at: string;
  env_override_active: boolean;
  env_override_image?: string;
}

export interface CatalogModelEntry {
  id?: number;
  hf_id: string;
  family?: string;
  enabled: boolean;
  updated_at?: string;
}

export interface CatalogInstanceTypeEntry {
  id?: number;
  name: string;
  enabled: boolean;
  updated_at?: string;
}

export interface CatalogMatrixPayload {
  defaults: CatalogSeedDefaults;
  models: CatalogModelEntry[];
  instance_types: CatalogInstanceTypeEntry[];
  version?: string;
}

// PRD-32: scenario overrides
export interface ScenarioOverride {
  scenario_id: string;
  num_workers?: number | null;
  streaming?: boolean | null;
  input_mean?: number | null;
  output_mean?: number | null;
  updated_at?: string;
}

export interface ScenarioOverrideEntry {
  scenario_id: string;
  name: string;
  defaults: {
    num_workers: number;
    streaming: boolean;
    input_mean: number;
    output_mean: number;
  };
  override?: ScenarioOverride;
  updated_at?: string;
}

// PRD-32: registry card
export interface RegistryRepoSummary {
  name: string;
  size_bytes: number;
  last_pulled_at?: string;
}

export interface RegistryStatus {
  enabled: boolean;
  uri?: string;
  repositories?: RegistryRepoSummary[];
  helm_hint?: string;
}

// PRD-32: audit log
export interface AuditLogEntry {
  id: number;
  at: string;
  action: string;
  actor?: string;
  summary: string;
}

// PRD-33: capacity reservations card
export interface ReservationSummary {
  id: string;
  type: string; // "default" | "capacity-block" | "unknown"
  state: string; // active | scheduled | expired | cancelled | pending | failed | payment-pending | payment-failed | unknown
  instance_type: string;
  availability_zone: string;
  total_instance_count: number;
  available_instance_count: number;
  start_date?: string;
  end_date?: string;
  end_date_type?: string;
  drain_warning_at?: string;
  tags?: Record<string, string>;
}

export interface NodePoolReservations {
  node_class: string;
  node_pool: string;
  instance_families: string[];
  subnet_azs: string[];
  capacity_type_includes_reserved: boolean;
  reservations: ReservationSummary[];
}

// PRD-35: Dashboard aggregates. One server-side query replaces the
// client-side tallies over listRuns() + listSuiteRuns() that used to power
// the stat cards.
export interface DashboardStats {
  total_runs: number;      // benchmark_runs + test_suite_runs
  total_single: number;
  total_suites: number;
  active_count: number;    // pending + running across both tables
  completed_count: number;
  failed_count: number;
  success_rate: number;    // completed / (completed + failed) × 100
  cached_models: number;
  total_cost_usd: number;  // lifetime
  cost_per_day: { day: string; cost_usd: number }[]; // 14 days, zero-filled
}

// PRD-35: ModelCache aggregates for the stat cards on the Models page.
// Needed because PRD-36 paginated the list, breaking client-side tallies.
export interface ModelCacheStats {
  total: number;
  cached: number;
  caching: number;    // includes pending
  failed: number;
  total_bytes: number;
}

// PRD-39: ?include= detail response types.
export interface RunDetailResponse extends BenchmarkRun {
  metrics?: BenchmarkMetrics;
  instance?: InstanceType;
  pricing?: PricingRow;
  oom?: OOMHistory;
  errors?: Record<string, string>;
}

export interface SuiteDetailResponse extends TestSuiteRun {
  instance?: InstanceType;
  pricing?: PricingRow;
  errors?: Record<string, string>;
}

// PRD-43: authenticated user. Populated from GET /api/v1/auth/me.
// `sub` was added in PRD-45 so the Users page can gate self-mutation
// row actions; omitted when the backend doesn't populate it.
export type AuthUser = {
  sub?: string;
  email: string;
  role: string;
};

// PRD-45: user-management types.
export interface CognitoUser {
  sub: string;
  email: string;
  role: string; // "" if unset on the Cognito user
  status: string; // Cognito UserStatusType verbatim
  enabled: boolean;
  created_at: string;
  last_modified_at: string;
}

export interface ListUsersResponse {
  rows: CognitoUser[];
  next_token?: string;
}
