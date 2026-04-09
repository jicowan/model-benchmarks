-- Add error_message column to benchmark_runs for diagnosing failures.
ALTER TABLE benchmark_runs ADD COLUMN IF NOT EXISTS error_message TEXT;
