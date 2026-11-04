package db

import (
	"context"
	"log"
	"time"
)

// StartPruner starts a background routine to purge check logs older than max retention.
func StartPruner(ctx context.Context, checkRepo *CheckRepo, logRepo *LogRepo, interval time.Duration) {
	ticker := time.NewTicker(interval)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				// Prune check logs older than 60 days
				checkRetention := time.Now().UTC().AddDate(0, 0, -60)
				deletedChecks, err := checkRepo.PruneOldLogs(checkRetention)
				if err != nil {
					log.Printf("[Pruner] Error pruning check logs: %v", err)
				} else if deletedChecks > 0 {
					log.Printf("[Pruner] Pruned %d check logs older than %s", deletedChecks, checkRetention.Format("2006-01-02"))
				}

				// Prune system logs older than 30 days
				sysLogRetention := time.Now().UTC().AddDate(0, 0, -30)
				deletedLogs, err := logRepo.PruneOldLogs(sysLogRetention)
				if err != nil {
					log.Printf("[Pruner] Error pruning system logs: %v", err)
				} else if deletedLogs > 0 {
					log.Printf("[Pruner] Pruned %d system logs older than %s", deletedLogs, sysLogRetention.Format("2006-01-02"))
				}
			}
		}
	}()
}
