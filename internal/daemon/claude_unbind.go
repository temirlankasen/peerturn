package daemon

import (
	"context"
	"github.com/temirlankasen/peerturn/internal/fault"
	"github.com/temirlankasen/peerturn/internal/localipc"
	"github.com/temirlankasen/peerturn/internal/store"
)

// claudeUnbind invalidates old authority before clearing the native UUID. A new
// native session must establish new grants, even though the local identity remains.
func (d *Daemon) claudeUnbind(ctx context.Context) (any, error) {
	if d.state.Codex != nil {
		return nil, localipc.NewError("unsupported", "unsupported")
	}
	if d.state.Reserved != nil {
		if err := d.recoverReserved(ctx); err != nil {
			return nil, err
		}
	}
	grants, err := d.db.ListGrants(ctx)
	if err != nil {
		return nil, err
	}
	for _, g := range grants {
		if !g.Revoked {
			if err = d.db.GrantRevokeLocal(ctx, g.GrantID, g.Epoch, unixNow()); err != nil {
				return nil, err
			}
		}
	}
	for id, entry := range d.state.Issued {
		if err = d.db.RevokeInvite(ctx, id); err != nil && !fault.IsCode(err, store.NotFound) {
			return nil, err
		}
		entry.Terminal, entry.Failure = true, "session_unbound"
	}
	for _, entry := range d.state.Joining {
		entry.Terminal, entry.Failure = true, "session_unbound"
	}
	for _, p := range d.state.Peers {
		p.Revoked, p.IncomingRevoked = true, true
	}
	d.state.NativeSessionID = ""
	d.state.ClaudeProject = ""
	d.cancelAskWorkers()
	d.capabilities = map[string]capabilityBinding{}
	d.claudeBinding, d.claudeAttempt, d.claudeNotice = nil, nil, nil
	if err = d.save(); err != nil {
		return nil, err
	}
	d.signal()
	return map[string]string{"state": "unbound"}, nil
}
