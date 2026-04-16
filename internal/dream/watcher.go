package dream

import (
	"context"
	"time"

	"golang.org/x/sync/errgroup"
)

// Watch runs the consolidation pass on a ticker until ctx is cancelled.
// An initial pass runs immediately so the first dream journal appears
// without waiting for the first tick. Returns nil on graceful shutdown.
//
// Interval <= 0 is a no-op (returns immediately). Use this to let the
// caller decide whether to start the daemon based on config.
func (s *Service) Watch(ctx context.Context, interval time.Duration) error {
	if interval <= 0 {
		return nil
	}

	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error {
		if _, err := s.Run(gctx, true); err != nil {
			s.log.Warn("initial dream pass failed", "err", err)
		}
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-gctx.Done():
				return nil
			case <-ticker.C:
				if _, err := s.Run(gctx, true); err != nil {
					s.log.Warn("dream pass failed", "err", err)
				}
			}
		}
	})
	return g.Wait()
}
