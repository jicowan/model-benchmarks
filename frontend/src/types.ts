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
  accelerator_memory_peak_gib?: number;
  successful_requests?: number;
  failed_requests?: number;
  total_duration_seconds?: number;
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
  dataset_name: string;
  run_type: string;
  max_model_len?: number;
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
