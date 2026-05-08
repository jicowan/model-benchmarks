package database

import (
	"context"
	"fmt"
)

// GetHostMemCalibration returns a map of observed p95 host-memory
// multipliers keyed by `"{model_family}|{loader}"` (loader is "hf" or
// "s3"). Only groups with >=3 completed runs are returned so one noisy
// observation can't swing the recommender.
//
// The ratio is peak_host_memory_gib / weight_size_gib. Weight size is
// computed from parameter_count × 2 bytes (BF16, which is what the HF
// loader materializes in host RAM before quantization kicks in).
// PRD-47 PR #5.
func (r *Repository) GetHostMemCalibration(ctx context.Context) (map[string]float64, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT
		    m.model_family,
		    CASE WHEN br.model_s3_uri IS NOT NULL THEN 's3' ELSE 'hf' END AS loader,
		    percentile_cont(0.95) WITHIN GROUP (
		        ORDER BY br.host_memory_peak_gib / (m.parameter_count * 2.0 / (1024.0^3))
		    ) AS p95_ratio,
		    COUNT(*) AS n_runs
		FROM benchmark_runs br
		JOIN models m ON m.id = br.model_id
		WHERE br.host_memory_peak_gib IS NOT NULL
		  AND br.status = 'completed'
		  AND m.parameter_count IS NOT NULL
		  AND m.parameter_count > 0
		  AND m.model_family IS NOT NULL
		GROUP BY m.model_family, loader
		HAVING COUNT(*) >= 3
	`)
	if err != nil {
		return nil, fmt.Errorf("query host mem calibration: %w", err)
	}
	defer rows.Close()

	out := make(map[string]float64)
	for rows.Next() {
		var family, loader string
		var ratio float64
		var n int
		if err := rows.Scan(&family, &loader, &ratio, &n); err != nil {
			return nil, fmt.Errorf("scan calibration row: %w", err)
		}
		if ratio <= 0 {
			continue
		}
		out[family+"|"+loader] = ratio
	}
	return out, rows.Err()
}
