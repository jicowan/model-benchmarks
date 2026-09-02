package main

import (
	"context"
	"log"
	"time"

	"github.com/accelbench/accelbench/internal/database"
)

// catalogRefreshInterval is how often the `catalog_rows` materialized view
// is refreshed. Every replica ticks, but PRD-68 P3 gates the refresh behind
// a Postgres advisory lock so only one replica does the work per tick.
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
				// PRD-68 P3: one replica refreshes per tick; the others skip.
				ran, err := repo.WithAdvisoryLock(ctx, database.LockKeyCatalogRefresh, repo.RefreshCatalogRows)
				if err != nil {
					log.Printf("[catalog-refresh] failed: %v", err)
					continue
				}
				if ran {
					log.Printf("[catalog-refresh] ok in %v", time.Since(start))
				}
			}
		}
	}()
}
