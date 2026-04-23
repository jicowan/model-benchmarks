package database

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// PricingRow is a denormalized view of pricing joined with instance type name.
type PricingRow struct {
	InstanceTypeName     string   `json:"instance_type_name"`
	OnDemandHourlyUSD    float64  `json:"on_demand_hourly_usd"`
	Reserved1YrHourlyUSD *float64 `json:"reserved_1yr_hourly_usd,omitempty"`
	Reserved3YrHourlyUSD *float64 `json:"reserved_3yr_hourly_usd,omitempty"`
	EffectiveDate        string   `json:"effective_date"`
}

// UpsertPricing inserts or updates a pricing row keyed by
// (instance_type_id, region, effective_date).
func (r *Repository) UpsertPricing(ctx context.Context, p *Pricing) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO pricing (instance_type_id, region, on_demand_hourly_usd,
		                     reserved_1yr_hourly_usd, reserved_3yr_hourly_usd, effective_date)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (instance_type_id, region, effective_date) DO UPDATE SET
			on_demand_hourly_usd    = EXCLUDED.on_demand_hourly_usd,
			reserved_1yr_hourly_usd = EXCLUDED.reserved_1yr_hourly_usd,
			reserved_3yr_hourly_usd = EXCLUDED.reserved_3yr_hourly_usd`,
		p.InstanceTypeID, p.Region, p.OnDemandHourlyUSD,
		p.Reserved1YrHourlyUSD, p.Reserved3YrHourlyUSD, p.EffectiveDate,
	)
	if err != nil {
		return fmt.Errorf("upsert pricing: %w", err)
	}
	return nil
}

// ListPricing returns the most recent pricing for each instance type in the
// given region, joined with the instance type name.
func (r *Repository) ListPricing(ctx context.Context, region string) ([]PricingRow, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT it.name, p.on_demand_hourly_usd, p.reserved_1yr_hourly_usd,
		       p.reserved_3yr_hourly_usd, p.effective_date::text
		FROM pricing p
		JOIN instance_types it ON it.id = p.instance_type_id
		WHERE p.region = $1
		  AND p.effective_date = (
		      SELECT MAX(p2.effective_date)
		      FROM pricing p2
		      WHERE p2.instance_type_id = p.instance_type_id AND p2.region = p.region
		  )
		ORDER BY it.name`, region)
	if err != nil {
		return nil, fmt.Errorf("list pricing: %w", err)
	}
	defer rows.Close()

	var result []PricingRow
	for rows.Next() {
		var pr PricingRow
		if err := rows.Scan(&pr.InstanceTypeName, &pr.OnDemandHourlyUSD,
			&pr.Reserved1YrHourlyUSD, &pr.Reserved3YrHourlyUSD, &pr.EffectiveDate); err != nil {
			return nil, fmt.Errorf("scan pricing row: %w", err)
		}
		result = append(result, pr)
	}
	return result, rows.Err()
}

// GetPricingForInstanceType returns the most recent pricing row for the given
// (instance_type_id, region) pair, or nil if no row exists. Used by the
// orchestrator at run completion (PRD-35) to freeze the hourly rate into
// benchmark_runs.total_cost_usd.
func (r *Repository) GetPricingForInstanceType(ctx context.Context, instanceTypeID, region string) (*Pricing, error) {
	var p Pricing
	err := r.pool.QueryRow(ctx, `
		SELECT id, instance_type_id, region, on_demand_hourly_usd,
		       reserved_1yr_hourly_usd, reserved_3yr_hourly_usd,
		       effective_date::text, created_at
		FROM pricing
		WHERE instance_type_id = $1 AND region = $2
		ORDER BY effective_date DESC
		LIMIT 1`, instanceTypeID, region).Scan(
		&p.ID, &p.InstanceTypeID, &p.Region, &p.OnDemandHourlyUSD,
		&p.Reserved1YrHourlyUSD, &p.Reserved3YrHourlyUSD,
		&p.EffectiveDate, &p.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get pricing for instance type: %w", err)
	}
	return &p, nil
}

// ListInstanceTypes returns all instance types.
func (r *Repository) ListInstanceTypes(ctx context.Context) ([]InstanceType, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, name, family, accelerator_type, accelerator_name,
		       accelerator_count, accelerator_memory_gib, vcpus, memory_gib
		FROM instance_types
		ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("list instance types: %w", err)
	}
	defer rows.Close()

	var result []InstanceType
	for rows.Next() {
		var it InstanceType
		if err := rows.Scan(&it.ID, &it.Name, &it.Family, &it.AcceleratorType,
			&it.AcceleratorName, &it.AcceleratorCount, &it.AcceleratorMemoryGiB,
			&it.VCPUs, &it.MemoryGiB); err != nil {
			return nil, fmt.Errorf("scan instance type: %w", err)
		}
		result = append(result, it)
	}
	return result, rows.Err()
}
