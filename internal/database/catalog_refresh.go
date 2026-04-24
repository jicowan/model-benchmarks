package database

import (
	"context"
	"fmt"
)

// RefreshCatalogRows runs REFRESH MATERIALIZED VIEW CONCURRENTLY on
// `catalog_rows`. Safe to call from any goroutine; Postgres serializes
// concurrent refreshes at the table level so overlap between multiple
// API pods is correct (the second waits for the first to finish).
//
// CONCURRENTLY holds a SHARE UPDATE EXCLUSIVE lock on the view — reads
// against the view keep working while the refresh runs.
func (r *Repository) RefreshCatalogRows(ctx context.Context) error {
	_, err := r.pool.Exec(ctx, `REFRESH MATERIALIZED VIEW CONCURRENTLY catalog_rows`)
	if err != nil {
		return fmt.Errorf("refresh catalog_rows: %w", err)
	}
	return nil
}
