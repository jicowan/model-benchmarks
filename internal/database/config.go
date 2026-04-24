package database

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// ErrStaleVersion is returned by PutCatalogMatrix when the DB has been
// modified since the caller's GET (optimistic concurrency check).
var ErrStaleVersion = errors.New("catalog matrix has been modified since last read")

// PutCatalogMatrix replaces the full matrix in a single transaction.
// `expectedVersion` is the `max(updated_at)` the caller saw on their
// previous GET; a 409 (ErrStaleVersion) is returned if any table has a
// newer `updated_at`, which signals another editor beat them to it.
//
// Semantics:
//   - `catalog_seed_defaults` is always present (the row with id=1 is
//     seeded by migration 018); we UPDATE it in place.
//   - `catalog_models` and `catalog_instance_types` are replaced: rows
//     missing from `m` are deleted; new rows are inserted; existing rows
//     are updated. We identify rows by their natural key (hf_id / name).
func (r *Repository) PutCatalogMatrix(ctx context.Context, m *CatalogMatrix, expectedVersion time.Time) error {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	// Optimistic concurrency: check current version before writing.
	var dbVersion time.Time
	err = tx.QueryRow(ctx, `
		SELECT GREATEST(
			(SELECT MAX(updated_at) FROM catalog_models),
			(SELECT MAX(updated_at) FROM catalog_instance_types),
			(SELECT updated_at FROM catalog_seed_defaults WHERE id = 1)
		)
	`).Scan(&dbVersion)
	if err != nil {
		return fmt.Errorf("read version: %w", err)
	}
	// Tolerate sub-millisecond clock skew — require the provided version to
	// be within 1s of the DB's.
	if !expectedVersion.IsZero() && dbVersion.Sub(expectedVersion) > time.Second {
		return ErrStaleVersion
	}

	// Update defaults.
	_, err = tx.Exec(ctx, `
		UPDATE catalog_seed_defaults
		   SET scenario = $1, dataset = $2, updated_at = now()
		 WHERE id = 1`,
		m.Defaults.Scenario, m.Defaults.Dataset)
	if err != nil {
		return fmt.Errorf("update defaults: %w", err)
	}

	// Replace models: delete rows not in the new set, upsert the rest.
	modelIDs := make([]string, 0, len(m.Models))
	for _, cm := range m.Models {
		modelIDs = append(modelIDs, cm.HfID)
		_, err := tx.Exec(ctx, `
			INSERT INTO catalog_models (hf_id, family, enabled, updated_at)
			VALUES ($1, $2, $3, now())
			ON CONFLICT (hf_id) DO UPDATE
			   SET family = EXCLUDED.family,
			       enabled = EXCLUDED.enabled,
			       updated_at = now()`,
			cm.HfID, cm.Family, cm.Enabled)
		if err != nil {
			return fmt.Errorf("upsert model %s: %w", cm.HfID, err)
		}
	}
	if len(modelIDs) == 0 {
		_, _ = tx.Exec(ctx, `DELETE FROM catalog_models`)
	} else {
		_, err = tx.Exec(ctx, `DELETE FROM catalog_models WHERE hf_id != ALL($1)`, modelIDs)
		if err != nil {
			return fmt.Errorf("prune models: %w", err)
		}
	}

	// Replace instance types similarly.
	instNames := make([]string, 0, len(m.InstanceTypes))
	for _, ci := range m.InstanceTypes {
		instNames = append(instNames, ci.Name)
		_, err := tx.Exec(ctx, `
			INSERT INTO catalog_instance_types (name, enabled, updated_at)
			VALUES ($1, $2, now())
			ON CONFLICT (name) DO UPDATE
			   SET enabled = EXCLUDED.enabled,
			       updated_at = now()`,
			ci.Name, ci.Enabled)
		if err != nil {
			return fmt.Errorf("upsert instance %s: %w", ci.Name, err)
		}
	}
	if len(instNames) == 0 {
		_, _ = tx.Exec(ctx, `DELETE FROM catalog_instance_types`)
	} else {
		_, err = tx.Exec(ctx, `DELETE FROM catalog_instance_types WHERE name != ALL($1)`, instNames)
		if err != nil {
			return fmt.Errorf("prune instance types: %w", err)
		}
	}

	return tx.Commit(ctx)
}

// --- Scenario overrides -----------------------------------------------------

func (r *Repository) ListScenarioOverrides(ctx context.Context) ([]ScenarioOverride, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT scenario_id, num_workers, streaming, input_mean, output_mean, updated_at
		   FROM scenario_overrides ORDER BY scenario_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ScenarioOverride
	for rows.Next() {
		var o ScenarioOverride
		if err := rows.Scan(&o.ScenarioID, &o.NumWorkers, &o.Streaming,
			&o.InputMean, &o.OutputMean, &o.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

func (r *Repository) GetScenarioOverride(ctx context.Context, scenarioID string) (*ScenarioOverride, error) {
	var o ScenarioOverride
	err := r.pool.QueryRow(ctx,
		`SELECT scenario_id, num_workers, streaming, input_mean, output_mean, updated_at
		   FROM scenario_overrides WHERE scenario_id = $1`, scenarioID).
		Scan(&o.ScenarioID, &o.NumWorkers, &o.Streaming, &o.InputMean, &o.OutputMean, &o.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &o, nil
}

func (r *Repository) UpsertScenarioOverride(ctx context.Context, o *ScenarioOverride) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO scenario_overrides
			(scenario_id, num_workers, streaming, input_mean, output_mean, updated_at)
		VALUES ($1, $2, $3, $4, $5, now())
		ON CONFLICT (scenario_id) DO UPDATE SET
			num_workers = EXCLUDED.num_workers,
			streaming   = EXCLUDED.streaming,
			input_mean  = EXCLUDED.input_mean,
			output_mean = EXCLUDED.output_mean,
			updated_at  = now()`,
		o.ScenarioID, o.NumWorkers, o.Streaming, o.InputMean, o.OutputMean)
	return err
}

func (r *Repository) DeleteScenarioOverride(ctx context.Context, scenarioID string) error {
	_, err := r.pool.Exec(ctx, `DELETE FROM scenario_overrides WHERE scenario_id = $1`, scenarioID)
	return err
}

// --- Audit log --------------------------------------------------------------

func (r *Repository) InsertAuditLog(ctx context.Context, action, summary string, actor *string) error {
	_, err := r.pool.Exec(ctx,
		`INSERT INTO config_audit_log (action, actor, summary) VALUES ($1, $2, $3)`,
		action, actor, summary)
	return err
}

func (r *Repository) ListAuditLog(ctx context.Context, limit int) ([]ConfigAuditEntry, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := r.pool.Query(ctx,
		`SELECT id, at, action, actor, summary
		   FROM config_audit_log ORDER BY at DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ConfigAuditEntry
	for rows.Next() {
		var e ConfigAuditEntry
		if err := rows.Scan(&e.ID, &e.At, &e.Action, &e.Actor, &e.Summary); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
