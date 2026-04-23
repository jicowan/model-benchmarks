package database

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// LoadCatalogMatrix reads the full seeding matrix in one pass. Returns
// all rows even when `enabled=false` — it's the seeder's job to skip
// disabled rows.
func (r *Repository) LoadCatalogMatrix(ctx context.Context) (*CatalogMatrix, error) {
	m := &CatalogMatrix{}

	// Defaults (singleton).
	err := r.pool.QueryRow(ctx,
		`SELECT scenario, dataset, min_duration_seconds, updated_at
		   FROM catalog_seed_defaults WHERE id = 1`).
		Scan(&m.Defaults.Scenario, &m.Defaults.Dataset,
			&m.Defaults.MinDurationSeconds, &m.Defaults.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("load catalog_seed_defaults: %w", err)
	}

	// Models.
	mrows, err := r.pool.Query(ctx,
		`SELECT id, hf_id, family, enabled, updated_at
		   FROM catalog_models ORDER BY hf_id`)
	if err != nil {
		return nil, fmt.Errorf("query catalog_models: %w", err)
	}
	defer mrows.Close()
	for mrows.Next() {
		var cm CatalogModel
		if err := mrows.Scan(&cm.ID, &cm.HfID, &cm.Family, &cm.Enabled, &cm.UpdatedAt); err != nil {
			return nil, err
		}
		m.Models = append(m.Models, cm)
	}
	if err := mrows.Err(); err != nil {
		return nil, err
	}

	// Instance types.
	irows, err := r.pool.Query(ctx,
		`SELECT id, name, enabled, updated_at
		   FROM catalog_instance_types ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("query catalog_instance_types: %w", err)
	}
	defer irows.Close()
	for irows.Next() {
		var ci CatalogInstanceType
		if err := irows.Scan(&ci.ID, &ci.Name, &ci.Enabled, &ci.UpdatedAt); err != nil {
			return nil, err
		}
		m.InstanceTypes = append(m.InstanceTypes, ci)
	}
	return m, irows.Err()
}

// ModelCacheByHfID returns every model_cache entry with a non-null hf_id,
// keyed by hf_id. The seeder uses this to decide whether to populate
// model_s3_uri on new runs.
func (r *Repository) ModelCacheByHfID(ctx context.Context) (map[string]ModelCache, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT id, hf_id, hf_revision, s3_uri, display_name, size_bytes,
		        status, error_message, job_name, cached_at, created_at
		   FROM model_cache
		  WHERE hf_id IS NOT NULL`)
	if err != nil {
		return nil, fmt.Errorf("query model_cache: %w", err)
	}
	defer rows.Close()
	out := make(map[string]ModelCache)
	for rows.Next() {
		var m ModelCache
		if err := rows.Scan(&m.ID, &m.HfID, &m.HfRevision, &m.S3URI, &m.DisplayName,
			&m.SizeBytes, &m.Status, &m.ErrorMessage, &m.JobName, &m.CachedAt,
			&m.CreatedAt); err != nil {
			return nil, err
		}
		if m.HfID != nil {
			out[*m.HfID] = m
		}
	}
	return out, rows.Err()
}

// ListRunKeys returns every (model_hf_id, instance_type_name) pair that has
// at least one non-failed benchmark run. Used by the seeder to dedup.
func (r *Repository) ListRunKeys(ctx context.Context) ([]RunKey, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT DISTINCT m.hf_id, it.name
		  FROM benchmark_runs br
		  JOIN models m         ON br.model_id = m.id
		  JOIN instance_types it ON br.instance_type_id = it.id
		 WHERE br.status != 'failed'`)
	if err != nil {
		return nil, fmt.Errorf("query run keys: %w", err)
	}
	defer rows.Close()
	var out []RunKey
	for rows.Next() {
		var k RunKey
		if err := rows.Scan(&k.ModelHfID, &k.InstanceTypeName); err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

func (r *Repository) CreateCatalogSeedStatus(ctx context.Context, id string, total int, dryRun bool) error {
	_, err := r.pool.Exec(ctx,
		`INSERT INTO catalog_seed_status (id, status, total, completed, dry_run)
		 VALUES ($1, 'active', $2, 0, $3)`,
		id, total, dryRun)
	return err
}

func (r *Repository) UpdateCatalogSeedProgress(ctx context.Context, id string, completed int) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE catalog_seed_status
		    SET completed = $2, updated_at = now()
		  WHERE id = $1`,
		id, completed)
	return err
}

func (r *Repository) CompleteCatalogSeedStatus(ctx context.Context, id string) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE catalog_seed_status
		    SET status = 'completed', updated_at = now(), completed_at = now()
		  WHERE id = $1`,
		id)
	return err
}

func (r *Repository) FailCatalogSeedStatus(ctx context.Context, id, errMsg string) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE catalog_seed_status
		    SET status = 'failed', error_message = $2, updated_at = now(), completed_at = now()
		  WHERE id = $1`,
		id, errMsg)
	return err
}

// InterruptActiveCatalogSeeds is called on API startup to reconcile goroutines
// that died with the pod. Any seed still marked `active` becomes `interrupted`.
func (r *Repository) InterruptActiveCatalogSeeds(ctx context.Context) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE catalog_seed_status
		    SET status = 'interrupted', updated_at = now(), completed_at = now()
		  WHERE status = 'active'`)
	return err
}

func (r *Repository) GetLatestCatalogSeedStatus(ctx context.Context) (*CatalogSeedStatus, error) {
	row := r.pool.QueryRow(ctx,
		`SELECT id, status, total, completed, dry_run, error_message,
		        started_at, updated_at, completed_at
		   FROM catalog_seed_status
		  ORDER BY started_at DESC
		  LIMIT 1`)
	var s CatalogSeedStatus
	if err := row.Scan(&s.ID, &s.Status, &s.Total, &s.Completed, &s.DryRun,
		&s.ErrorMessage, &s.StartedAt, &s.UpdatedAt, &s.CompletedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &s, nil
}

func (r *Repository) GetActiveCatalogSeed(ctx context.Context) (*CatalogSeedStatus, error) {
	row := r.pool.QueryRow(ctx,
		`SELECT id, status, total, completed, dry_run, error_message,
		        started_at, updated_at, completed_at
		   FROM catalog_seed_status
		  WHERE status = 'active'
		  LIMIT 1`)
	var s CatalogSeedStatus
	if err := row.Scan(&s.ID, &s.Status, &s.Total, &s.Completed, &s.DryRun,
		&s.ErrorMessage, &s.StartedAt, &s.UpdatedAt, &s.CompletedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &s, nil
}
