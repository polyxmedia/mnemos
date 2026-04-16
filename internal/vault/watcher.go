package vault

import (
	"context"
	"fmt"
	"time"
)

// Watch runs the exporter on a ticker until ctx is cancelled. Each tick
// performs a full export (cheap; SQLite is fast and writes are rare on
// the vault side). If interval is <= 0, Watch returns immediately with
// no error — caller can treat that as "watch disabled".
func (e *Exporter) Watch(ctx context.Context, interval time.Duration) error {
	if interval <= 0 {
		return nil
	}
	// Immediate first sync so the user sees output right away.
	if _, err := e.ExportAll(ctx); err != nil {
		e.log.Warn("vault initial export failed", "err", err)
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			stats, err := e.ExportAll(ctx)
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
