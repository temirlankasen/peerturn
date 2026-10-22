package daemon

import (
	"context"

	"github.com/temirlankasen/peerturn/internal/grant"
)

// A never-relayed expired sequence cannot be skipped by guessing what the peer
// saw. Fence only that outgoing channel and require explicit fresh binding.
func (d *Daemon) maintain(ctx context.Context) error {
	if err := d.pruneOnboarding(); err != nil {
		return err
	}
	now := unixNow()
	expired, err := d.db.ExpiredOutboxGrants(ctx, now)
	if err != nil {
		return err
	}
	if len(expired) > 0 {
		grants, readErr := d.db.ListGrants(ctx)
		if readErr != nil {
			return readErr
		}
		stale := map[string]bool{}
		for _, id := range expired {
			stale[id] = true
		}
		changed := false
		for _, g := range grants {
			if !stale[g.GrantID] {
				continue
			}
			for _, peer := range d.state.Peers {
				if peer.IncomingRevoked || peer.Revoked {
					continue
				}
				if g.Hash == grant.Hash(peer.Incoming) || g.Hash == grant.Hash(peer.Outgoing) {
					if !peer.SequenceBlocked {
						peer.SequenceBlocked = true
						changed = true
					}
				}
			}
		}
		if changed {
			if err = d.save(); err != nil {
				return err
			}
		}
	}
	if len(expired) > 0 || now-d.lastMaintenance >= 60 {
		if err = d.db.Expire(ctx, now); err != nil {
			return err
		}
		d.lastMaintenance = now
	}
	return nil
}
