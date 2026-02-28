ALTER TABLE benchmark_metrics ADD COLUMN accelerator_utilization_avg_pct NUMERIC;
ALTER TABLE benchmark_metrics ADD COLUMN waiting_requests_max INT;
ALTER TABLE benchmark_runs ADD COLUMN min_duration_seconds INT DEFAULT 180;
