-- Persist model_s3_uri on benchmark_runs so exported K8s manifests can
-- emit the correct --load-format and --model arguments for runs that
-- loaded weights from S3 (vs. directly from HuggingFace).
ALTER TABLE benchmark_runs
    ADD COLUMN IF NOT EXISTS model_s3_uri TEXT;
