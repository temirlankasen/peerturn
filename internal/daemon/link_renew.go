package daemon

import (
	"context"
	"errors"

	"github.com/temirlankasen/peerturn/internal/fault"
	"github.com/temirlankasen/peerturn/internal/grant"
	"github.com/temirlankasen/peerturn/internal/identity"
	"github.com/temirlankasen/peerturn/internal/protocol"
	"github.com/temirlankasen/peerturn/internal/relayclient"
	"github.com/temirlankasen/peerturn/internal/store"
)

// floorGrace tolerates clock skew before an own expired packet counts as lost.
const floorGrace int64 = 10 * 60

func (p *peerState) incomingGrants() []string { return liveSigned(p.Incoming, p.PriorIncoming) }
func (p *peerState) outgoingGrants() []string { return liveSigned(p.Outgoing, p.PriorOutgoing) }

func liveSigned(current, prior string) []string {
	if prior == "" {
		return []string{current}
	}
	return []string{current, prior}
}

func (p *peerState) hasIncoming(hash string) bool {
	for _, signed := range p.incomingGrants() {
		if grant.Hash(signed) == hash {
			return true
		}
	}
	return false
}

// incomingFor selects the incoming grant a packet names. Packets sealed before
// the peer installed a renewal still name the prior grant.
func (p *peerState) incomingFor(hash string) string {
	if p.PriorIncoming != "" && grant.Hash(p.PriorIncoming) == hash {
		return p.PriorIncoming
	}
	return p.Incoming
}

func (p *peerState) connected() bool {
	return p.HelloSent && p.HelloReceived && !p.Revoked && !p.IncomingRevoked
}

// renewLink reissues the grant this daemon gave the peer once the link is in
// use and the grant has less than half its lifetime left. The renewed grant
// keeps scope, limits and reply route; only its window moves.
func (d *Daemon) renewLink(ctx context.Context, p *peerState, used string) error {
	if !p.connected() {
		return nil
	}
	now := unixNow()
	// The peer still seals under the prior grant: the update was lost.
	if used != p.Incoming && p.Update == nil && now-p.UpdatedAt >= floorGrace {
		return d.scheduleUpdate(p)
	}
	cert, err := identity.VerifyChain(p.Chain, identity.RootFingerprint(p.Chain.RootPublicKey), now)
	if err != nil {
		return nil
	}
	current, err := grant.Verify(p.Incoming, d.cert, cert, 1, now)
	if err != nil || current.ExpiresAt-now >= grant.DefaultLifetime/2 {
		return nil
	}
	next := current
	next.GrantID, next.NotBefore = randomID(), now
	next.ExpiresAt = min(now+grant.DefaultLifetime, d.cert.ExpiresAt, cert.ExpiresAt)
	if next.ExpiresAt <= current.ExpiresAt {
		return nil
	}
	signed, err := grant.Issue(next, d.signingKey())
	if err != nil {
		return err
	}
	if err = d.installGrant(ctx, signed, d.state.Chain, p.Chain); err != nil {
		return err
	}
	p.PriorIncoming, p.Incoming = p.Incoming, signed
	d.logger().Info("grant renewed", "peer", shortID(peerID(p.Chain)), "expires_at", next.ExpiresAt)
	return d.scheduleUpdate(p)
}

// scheduleUpdate builds one link update carrying the current incoming grant
// and the announced floor. It replaces an unsent update, which is a subset.
func (d *Daemon) scheduleUpdate(p *peerState) error {
	// Without a live grant from the peer there is no lane for an update; the
	// state is still saved, and the next use or loss check retries.
	if packet, err := d.handshakePacket(p, handshake{Grant: p.Incoming, Floor: p.Floor}); err == nil {
		p.Update = &packet
	}
	return d.save()
}

// syncLinkUpdates hands built updates to the relay. A network failure keeps
// the update for the next sync; a relay refusal drops it, and the next use or
// loss check builds a fresh one.
func (d *Daemon) syncLinkUpdates(ctx context.Context) error {
	for _, p := range d.state.Peers {
		if p.Update == nil {
			continue
		}
		if p.connected() {
			_, err := networkCall(ctx, d, func() (relayclient.Receipt, error) { return d.relay.Send(ctx, *p.Update) })
			if retryCustody(err) {
				return err
			}
			if problem, ok := errors.AsType[*relayclient.Error](err); err != nil && (!ok || problem.Status >= 500) {
				continue
			}
			if err == nil {
				p.UpdatedAt = unixNow()
			}
		}
		p.Update = nil
		if err := d.save(); err != nil {
			return err
		}
	}
	return nil
}

// announceLoss tells each peer the highest own sequence it may stop waiting
// for. The relay mailbox keeps order and the peer commits each mailbox entry
// before the next, so every packet relayed before the update reaches the peer
// first: it is admitted, held parked, or lost. Only a packet still waiting
// for the relay could follow the update, so the floor stays below the lowest
// one that has not expired. It replaces the old fence, which needed a new
// invitation.
func (d *Daemon) announceLoss(ctx context.Context) error {
	now := unixNow()
	for id, p := range d.state.Peers {
		if !p.connected() || p.Update != nil {
			continue
		}
		cert, err := identity.VerifyChain(p.Chain, identity.RootFingerprint(p.Chain.RootPublicKey), now)
		if err != nil {
			continue
		}
		lost, err := d.db.OutboundLoss(ctx, d.cert.Binding, cert.Binding, now-d.lossGrace)
		if err != nil {
			return err
		}
		floor, err := d.unrelayedFloor(ctx, id, now-d.lossGrace)
		if err != nil {
			return err
		}
		// A lost receipt is not observable, so a floor that grew without a lost
		// data packet still goes out daily. An announced floor is repeated
		// before its update could expire unread. A fence from an older version
		// is lifted the same way.
		grown := floor > p.Floor && (lost || now-p.UpdatedAt >= 24*60*60)
		repeat := max(p.Floor, floor) > 0 && now-p.UpdatedAt >= messageTTL/2
		if !grown && !repeat && !p.SequenceBlocked {
			continue
		}
		p.SequenceBlocked = false
		if floor == 0 {
			if err = d.save(); err != nil {
				return err
			}
			continue
		}
		p.Floor = max(p.Floor, floor)
		d.logger().Warn("lost sequences announced", "peer", shortID(id), "floor", p.Floor)
		if err = d.scheduleUpdate(p); err != nil {
			return err
		}
	}
	return nil
}

// unrelayedFloor is the highest own sequence to peer below every packet that
// still waits for the relay and has not expired by before.
func (d *Daemon) unrelayedFloor(ctx context.Context, peer string, before int64) (uint64, error) {
	floor := d.state.Sequences[peer]
	if r := d.state.Reserved; r != nil && r.PeerID == peer && r.Message.SenderSeq > 0 {
		floor = min(floor, r.Message.SenderSeq-1)
	}
	for messageID, order := range d.state.Pending {
		if order.PeerID != peer || order.SenderSeq > floor {
			continue
		}
		live, err := d.db.OutboxLive(ctx, messageID, before)
		if err != nil {
			return 0, err
		}
		if live && order.SenderSeq > 0 {
			floor = order.SenderSeq - 1
		}
	}
	return floor, nil
}

// acceptLinkUpdate applies an authenticated update from the exact peer: a
// renewed grant for this daemon's sends and a floor over the peer's own lost
// sequences. Replays are harmless because both only move forward.
func (d *Daemon) acceptLinkUpdate(ctx context.Context, p *peerState, cert identity.SessionCertificate, u handshake) error {
	if u.InviteID != "" || u.ClaimID != "" || u.ForwardGrant != "" || u.ReturnGrantHash != "" || u.NextSeq != 0 {
		return invalidHandshake()
	}
	if u.Heartbeat {
		return d.acceptHeartbeat(p, u)
	}
	if u.Grant != "" {
		if err := d.acceptRenewedGrant(ctx, p, cert, u.Grant); err != nil {
			return err
		}
	}
	return d.skipLost(ctx, p, cert, u.Floor)
}

// skipLost raises the replay floor over the peer's missing sequences up to
// floor but never over a packet held parked: each one is admitted in turn.
// A packet that stays parked stops the floor below it.
func (d *Daemon) skipLost(ctx context.Context, p *peerState, cert identity.SessionCertificate, floor uint64) error {
	if floor == 0 {
		return nil
	}
	if floor > protocol.MaxSafeInteger {
		return invalidHandshake()
	}
	for {
		held, ok, err := d.db.ParkedAtOrBelow(ctx, cert.Binding, d.cert.Binding, floor)
		if err != nil {
			return err
		}
		next := floor
		if ok {
			next = held - 1
		}
		if err = d.db.RaiseReplayFloor(ctx, cert.Binding, d.cert.Binding, next); err != nil {
			return err
		}
		if err = d.recheckParked(ctx, p); err != nil {
			return err
		}
		if !ok {
			return nil
		}
		again, still, err := d.db.ParkedAtOrBelow(ctx, cert.Binding, d.cert.Binding, floor)
		if err != nil || still && again <= held {
			return err
		}
	}
}

func (d *Daemon) acceptRenewedGrant(ctx context.Context, p *peerState, cert identity.SessionCertificate, signed string) error {
	if signed == p.Outgoing || signed == p.PriorOutgoing {
		return nil
	}
	next, err := grant.Verify(signed, cert, d.cert, 1, unixNow())
	if err != nil {
		return err
	}
	current, err := grant.Authenticate(p.Outgoing, cert)
	if err != nil {
		return err
	}
	if next.Scope != current.Scope || next.Limits != current.Limits || !sameRoute(next.ReplyRoute, current.ReplyRoute) {
		return invalidHandshake()
	}
	if next.ExpiresAt <= current.ExpiresAt {
		return nil
	}
	if err = d.installGrant(ctx, signed, p.Chain, d.state.Chain); err != nil {
		return err
	}
	p.PriorOutgoing, p.Outgoing = p.Outgoing, signed
	d.logger().Info("renewed grant installed", "peer", shortID(peerID(p.Chain)), "expires_at", next.ExpiresAt)
	return d.save()
}

func sameRoute(a, b *identity.Binding) bool {
	if a == nil || b == nil {
		return a == b
	}
	return identity.EqualBinding(*a, *b)
}

// recheckParked promotes parked packets under both grants. Releasing one
// grant's packet can unblock the other's, so passes repeat while any packet
// leaves the gap; the bound only guards against a store that never settles.
func (d *Daemon) recheckParked(ctx context.Context, p *peerState) error {
	for range 16 {
		progress := false
		for _, signed := range p.incomingGrants() {
			g, err := d.incomingGrantFor(p, signed)
			if err != nil {
				continue
			}
			results, err := d.db.RecheckParked(ctx, store.InboxRequest{GrantID: g.GrantID, Mailbox: d.mailbox(), Open: d.openOptionsFor(p, signed)})
			if err != nil {
				return err
			}
			for _, r := range results {
				progress = progress || r.Stage != string(fault.SequenceGap)
			}
		}
		if !progress || p.PriorIncoming == "" {
			return nil
		}
	}
	return nil
}

func (p *peerState) outgoingHashes() []string {
	hashes := []string{}
	for _, signed := range p.outgoingGrants() {
		hashes = append(hashes, grant.Hash(signed))
	}
	return hashes
}

func (d *Daemon) installedGrantID(ctx context.Context, signed string) (string, error) {
	grants, err := d.db.ListGrants(ctx)
	if err != nil {
		return "", err
	}
	for _, g := range grants {
		if g.Hash == grant.Hash(signed) {
			return g.GrantID, nil
		}
	}
	return "", fault.New(store.NotFound)
}

// revokePriorIncoming ends a renewed grant's predecessor like a superseded
// link: no native delivery or ask may continue under it.
func (d *Daemon) revokePriorIncoming(ctx context.Context, p *peerState) error {
	if p.PriorIncoming == "" {
		return nil
	}
	grants, err := d.db.ListGrants(ctx)
	if err != nil {
		return err
	}
	for _, g := range grants {
		if g.Revoked || g.Hash != grant.Hash(p.PriorIncoming) {
			continue
		}
		if err = d.deleteNativeGrant(ctx, g.GrantID); err != nil {
			return err
		}
		if err = d.db.GrantRevokeLocal(ctx, g.GrantID, g.Epoch, unixNow()); err != nil {
			return err
		}
		d.cancelAskWorkers()
	}
	return nil
}

// incomingGrantByID finds the live incoming grant, current or prior, that
// admitted a stored message.
func (d *Daemon) incomingGrantByID(p *peerState, id string) (grant.Grant, bool) {
	for _, signed := range p.incomingGrants() {
		if g, err := d.incomingGrantFor(p, signed); err == nil && g.GrantID == id {
			return g, true
		}
	}
	return grant.Grant{}, false
}
