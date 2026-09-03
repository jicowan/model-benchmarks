package main

import (
	"context"
	"log"
	"os"
	"strconv"
	"time"

	"github.com/accelbench/accelbench/internal/database"
)

// PRD-68 P6: opt-in retention sweep. RUN_RETENTION_DAYS=0 (default) disables
// it. When set, one replica (advisory lock) deletes terminal runs/suites
// older than N days in batches of 500, once an hour, until a pass deletes
// nothing.
func StartRetentionLoop(ctx context.Context, repo database.Repo) {
	days := 0
	if v := os.Getenv("RUN_RETENTION_DAYS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			log.Printf("warning: RUN_RETENTION_DAYS=%q invalid; retention disabled", v)
		} else {
			days = n
		}
	}
	if days == 0 {
		return
	}
	log.Printf("[retention] enabled: terminal runs older than %d days are purged hourly", days)
	go func() {
		t := time.NewTicker(time.Hour)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
			_, err := repo.WithAdvisoryLock(ctx, database.LockKeyRetention, func(ctx context.Context) error {
				for i := 0; i < 20; i++ { // ≤ 10k rows per hourly pass
					n, err := repo.PurgeTerminalRunsOlderThan(ctx, days, 500)
					if err != nil {
						return err
					}
					if n == 0 {
						return nil
					}
					log.Printf("[retention] purged %d run(s)/suite(s) older than %d days", n, days)
				}
				return nil
			})
			if err != nil {
				log.Printf("[retention] pass failed: %v", err)
			}
		}
	}()
}
