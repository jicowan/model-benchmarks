-- Add unique constraint on pricing to support upserts.
CREATE UNIQUE INDEX IF NOT EXISTS idx_pricing_instance_region_date
    ON pricing (instance_type_id, region, effective_date);
