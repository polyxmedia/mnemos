package vault

import (
	"context"
	"fmt"
	"time"

	"golang.org/x/sync/errgroup"
)

// Watch runs the exporter on a ticker until ctx is cancelled. An initial
// full export runs immediately so the user sees output right away. Each
// tick performs a full export (cheap for realistic sizes; incremental
// sync is handled inside ExportAll via last_exported_at).
//
// Interval <= 0 is a no-op — treat as "watch disabled".
func (e *Exporter) Watch(ctx context.Context, interval time.Duration) error {
	if interval <= 0 {
		return nil
	}
	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error {
		if _, err := e.ExportAll(gctx); err != nil {
			e.log.Warn("vault initial export failed", "err", err)
		}
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-gctx.Done():
				return nil
			case <-ticker.C:
				stats, err := e.ExportAll(gctx)
				if err != nil {
					e.log.Warn("vault export failed", "err", err)
					continue
				}
				e.log.Info("vault export",
					"observations", stats.Observations,
					"sessions", stats.Sessions,
					"skills", stats.Skills,
					"tags", stats.Tags)
			}
		}
	})
	return g.Wait()
}

// ParseInterval wraps time.ParseDuration with a friendlier error so the
// CLI can produce a clean message for bad [vault].watch_interval values.
func ParseInterval(s string) (time.Duration, error) {
	if s == "" {
		return 0, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("watch_interval %q: %w (use Go durations like 5m, 1h)", s, err)
	}
	return d, nil
}
