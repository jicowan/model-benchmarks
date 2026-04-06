-- Add GPU metrics to scenario_results (matching benchmark_metrics fields)
ALTER TABLE scenario_results ADD COLUMN IF NOT EXISTS accelerator_utilization_pct DOUBLE PRECISION;
ALTER TABLE scenario_results ADD COLUMN IF NOT EXISTS accelerator_memory_peak_gib DOUBLE PRECISION;
