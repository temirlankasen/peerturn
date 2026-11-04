package daemon

import (
	"context"

	"github.com/temirlankasen/peerturn/internal/grant"
	"github.com/temirlankasen/peerturn/internal/identity"
)

// supersededPeer names the record that a new owner-approved invitation from
// the same peer may replace: the exact chain after a Claude session change,
// or the next session epoch after certificate renewal. Refusing either would
// leave the pair permanently unlinkable. Unfinished onboarding or a reserved
// packet keeps the old record in charge.
func (d *Daemon) supersededPeer(chain identity.Chain, route []byte) string {
	if id := d.predecessor(chain, route); id != "" && d.replaceable(id) {
		return id
	}
	return ""
}

// predecessor finds the record of the same peer session. Route keys are
// unique across records, so at most one record can match.
func (d *Daemon) predecessor(chain identity.Chain, route []byte) string {
	for id, old := range d.state.Peers {
		if equalBytes(old.RoutingKey, route) && equalBytes(old.Chain.RootPublicKey, chain.RootPublicKey) && sameSession(old.Chain, chain) {
			return id
		}
	}
	return ""
}

func sameSession(old, next identity.Chain) bool {
	if equalBytes(old.RootPublicKey, next.RootPublicKey) && old.Device == next.Device && old.Session == next.Session {
		return true
	}
	before, _, err := historicalBinding(old)
	if err != nil {
		return false
	}
	after, _, err := historicalBinding(next)
	if err != nil {
		return false
	}
	return before.IdentityID == after.IdentityID && before.DeviceID == after.DeviceID && before.DeviceEpoch == after.DeviceEpoch &&
		before.SessionID == after.SessionID && after.SessionEpoch > before.SessionEpoch
}

func (d *Daemon) replaceable(id string) bool {
	if d.state.Reserved != nil && d.state.Reserved.PeerID == id {
		return false
	}
	for _, j := range d.state.Joining {
		if !j.Terminal && !j.Complete && peerID(j.Descriptor.Inviter) == id {
			return false
		}
	}
	for _, i := range d.state.Issued {
		if !i.Terminal && i.Claim != nil && !i.Sent && peerID(i.Claim.Claimant) == id {
			return false
		}
	}
	return true
}

// replacePeer installs the record for a new link. The old link's grants are
// revoked locally first: a delivery reserved under them must not outlive a
// later revoke of the new link, which only finds the current grant. Unread
// messages of the replaced link therefore leave the inbox. Unsent packets are
// dropped and the relay route moves to a later permit epoch. On the same
// binding the sender counter survives and the handshake announces where it
// continues; a renewed epoch is a new channel and starts from the beginning.
func (d *Daemon) replacePeer(ctx context.Context, next *peerState) error {
	id := peerID(next.Chain)
	// checkPeerBinding already admitted the replacement; the claim being
	// accepted now counts as unfinished onboarding for this very peer.
	previous := d.predecessor(next.Chain, next.RoutingKey)
	if previous == "" {
		d.state.Peers[id] = next
		return nil
	}
	old := d.state.Peers[previous]
	if err := d.revokeLink(ctx, old); err != nil {
		return err
	}
	for messageID, order := range d.state.Pending {
		if order.PeerID == previous {
			delete(d.state.Pending, messageID)
		}
	}
	next.PermitEpoch = old.permitEpoch() + 2
	if previous != id {
		delete(d.state.Peers, previous)
		delete(d.state.Sequences, previous)
	}
	d.state.Peers[id] = next
	return nil
}

func (d *Daemon) revokeLink(ctx context.Context, old *peerState) error {
	superseded := map[string]bool{}
	for _, signed := range append(old.incomingGrants(), old.outgoingGrants()...) {
		if signed != "" {
			superseded[grant.Hash(signed)] = true
		}
	}
	grants, err := d.db.ListGrants(ctx)
	if err != nil {
		return err
	}
	revoked := false
	for _, g := range grants {
		if g.Revoked || !superseded[g.Hash] {
			continue
		}
		if old.hasIncoming(g.Hash) {
			if err = d.deleteNativeGrant(ctx, g.GrantID); err != nil {
				return err
			}
		}
		if err = d.db.GrantRevokeLocal(ctx, g.GrantID, g.Epoch, unixNow()); err != nil {
			return err
		}
		revoked = true
	}
	if revoked {
		d.cancelAskWorkers()
	}
	return nil
}

func (p *peerState) permitEpoch() int64 { return max(p.PermitEpoch, 1) }
