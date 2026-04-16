package dream

import (
	"context"
	"time"
)

// Watch runs the consolidation pass on a ticker until ctx is cancelled.
// Each tick writes a dream-journal observation if anything changed.
func (s *Service) Watch(ctx context.Context, interval time.Duration) error {
	if interval <= 0 {
		return nil
	}
	if _, err := s.Run(ctx, true); err != nil {
		s.log.Warn("initial dream pass failed", "err", err)
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if _, err := s.Run(ctx, true); err != nil {
				s.log.Warn("dream pass failed", "err", err)
			}
		}
	}
}
