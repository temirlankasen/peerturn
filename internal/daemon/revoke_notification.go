package daemon

import (
	"context"
	"errors"
	"net/http"

	"github.com/temirlankasen/peerturn/internal/fault"
	"github.com/temirlankasen/peerturn/internal/grant"
	"github.com/temirlankasen/peerturn/internal/relayclient"
)

func revokedRouteDenial(err error) bool {
	problem, ok := errors.AsType[*relayclient.Error](err)
	return ok && (problem.Status == http.StatusForbidden || problem.Status == http.StatusGone)
}

func (d *Daemon) stopRevokedReceipt(ctx context.Context, id string, peer *peerState) error {
	grants, err := d.db.ListGrants(ctx)
	if err != nil {
		return err
	}
	for _, g := range grants {
		if !g.Revoked || g.Hash != grant.Hash(peer.Incoming) {
			continue
		}
		if err = d.db.StopRevokedReceipt(ctx, id, g.GrantID, unixNow()); err != nil {
			return err
		}
		peer.NotificationsUnavailable = true
		delete(d.state.Pending, id)
		return d.save()
	}
	return fault.New(fault.Conflict)
}
