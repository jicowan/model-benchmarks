package database

import (
	"context"
	"fmt"
)

// GetHostMemCalibration returns a map of observed p95 host-memory
// multipliers keyed by `"{model_type}|{loader}"` (loader is "hf" or
// "s3"). Only groups with >=3 completed runs are returned so one noisy
// observation can't swing the recommender.
//
// PRD-51: the ratio is NON-STREAMER host RAM per weight byte. For
// streamer-on runs, we subtract the streamer's CPU buffer from the
// observed peak before dividing by weight size. The recommender adds
// the streamer term back at prediction time, gated on whether the
// predicted run will use the streamer. Without the split, the
// multiplier bundles streamer overhead and mispredicts streamer-off
// (or HF-download) runs.
//
// Historical runs (pre-PRD-50) have no streamer_memory_limit_gib
// column, so we backfill with `min(weight, 40 GiB)` — the upstream
// default when the env var is unset. See PRD-51 design note.
//
// Weight size is computed from parameter_count × 2 bytes (BF16, which
// is what the HF loader materializes in host RAM before quantization).
// PRD-47 PR #5; follow-up renamed model_family → model_type;
// PRD-51 added the streamer-term subtraction.
func (r *Repository) GetHostMemCalibration(ctx context.Context) (map[string]float64, error) {
	// weight_gib = parameter_count × 2 / 1024^3
	// streamer_buf_gib = CASE ... END   (0 for hf / streamer-off; min(weight, limit_or_40) otherwise)
	// non_streamer_peak_gib = GREATEST(0, host_memory_peak_gib - streamer_buf_gib)
	// ratio = non_streamer_peak_gib / weight_gib
	rows, err := r.pool.Query(ctx, `
		WITH run_samples AS (
		    SELECT
		        m.model_type,
		        CASE WHEN br.model_s3_uri IS NOT NULL THEN 's3' ELSE 'hf' END AS loader,
		        br.host_memory_peak_gib AS peak_gib,
		        (m.parameter_count * 2.0 / (1024.0^3)) AS weight_gib,
		        CASE
		            WHEN br.streamer_mode = 'off' THEN 0
		            WHEN br.model_s3_uri IS NULL THEN 0
		            WHEN br.streamer_memory_limit_gib IS NOT NULL
		                THEN LEAST(m.parameter_count * 2.0 / (1024.0^3), br.streamer_memory_limit_gib::numeric)
		            ELSE LEAST(m.parameter_count * 2.0 / (1024.0^3), 40.0)
		        END AS streamer_buf_gib
		    FROM benchmark_runs br
		    JOIN models m ON m.id = br.model_id
		    WHERE br.host_memory_peak_gib IS NOT NULL
		      AND br.status = 'completed'
		      AND m.parameter_count IS NOT NULL
		      AND m.parameter_count > 0
		      AND m.model_type IS NOT NULL
		)
		SELECT
		    model_type,
		    loader,
		    percentile_cont(0.95) WITHIN GROUP (
		        ORDER BY GREATEST(0, peak_gib - streamer_buf_gib) / weight_gib
		    ) AS p95_ratio,
		    COUNT(*) AS n_runs
		FROM run_samples
		GROUP BY model_type, loader
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
