package main

import (
	"context"
	"log"
	"time"

	"github.com/accelbench/accelbench/internal/database"
)

// catalogRefreshInterval is how often each API pod asks Postgres to
// refresh the `catalog_rows` materialized view. With two replicas both
// pods tick independently; REFRESH CONCURRENTLY serializes at the DB
// so overlap is safe. At current view size the doubled cost is
// negligible.
const catalogRefreshInterval = 5 * time.Minute

// StartCatalogRefreshLoop runs one synchronous REFRESH before returning,
// then kicks off a background goroutine that refreshes every
// catalogRefreshInterval. The initial refresh runs synchronously so the
// HTTP listener never starts serving an empty Catalog page on a cold
// deploy. If Postgres is unavailable at startup, the initial refresh
// error is logged but not fatal — the ticker retries on the next tick.
func StartCatalogRefreshLoop(ctx context.Context, repo database.Repo) {
	log.Printf("[catalog-refresh] running initial refresh")
	if err := repo.RefreshCatalogRows(ctx); err != nil {
		log.Printf("[catalog-refresh] initial refresh failed: %v", err)
	}

	go func() {
		t := time.NewTicker(catalogRefreshInterval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				start := time.Now()
				if err := repo.RefreshCatalogRows(ctx); err != nil {
					log.Printf("[catalog-refresh] failed: %v", err)
					continue
				}
				log.Printf("[catalog-refresh] ok in %v", time.Since(start))
			}
		}
	}()
}
