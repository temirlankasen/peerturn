package relay

import (
	"context"
	"time"
)

// Idle relays reclaim expiry space too; cleanup does not depend on admitting a
// new message and cannot be rolled back by that message's quota failure.
func (s *Server) startMaintenance() {
	s.maintenance.Go(func() {
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-s.ctx.Done():
				return
			case <-ticker.C:
				ctx, cancel := context.WithTimeout(s.ctx, 5*time.Second)
				if err := s.db.Sweep(ctx, s.now()); err != nil {
					s.sweepFailures.Add(1)
				}
				cancel()
			}
		}
	})
}
