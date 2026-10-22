package daemon

import (
	"context"

	"github.com/temirlankasen/peerturn/internal/grant"
)

// The queue commit is authoritative across parked promotion and journal crashes.
// A cached peer flag is only a durable projection of authenticated receipt data.
func (d *Daemon) refreshPeerRevocations(ctx context.Context) error {
	grants, err := d.db.ListGrants(ctx)
	if err != nil {
		return err
	}
	changed := false
	for _, installed := range grants {
		for _, peer := range d.state.Peers {
			if peer.Revoked || installed.Hash != grant.Hash(peer.Outgoing) {
				continue
			}
			revoked, queryErr := d.db.RevokedOutboundGrant(ctx, installed.GrantID)
			if queryErr != nil {
				return queryErr
			}
			if revoked {
				peer.Revoked = true
				changed = true
			}
		}
	}
	if changed {
		d.cancelAskWorkers()
		return d.save()
	}
	return nil
}
