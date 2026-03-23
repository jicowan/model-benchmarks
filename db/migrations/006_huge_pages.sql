-- Add huge_pages_enabled column to benchmark_runs
ALTER TABLE benchmark_runs ADD COLUMN IF NOT EXISTS huge_pages_enabled BOOLEAN NOT NULL DEFAULT FALSE;
