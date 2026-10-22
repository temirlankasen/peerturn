package daemon

import (
	"context"
	"crypto/ecdh"
	"errors"

	"github.com/temirlankasen/peerturn/internal/codec"
	"github.com/temirlankasen/peerturn/internal/envelope"
	"github.com/temirlankasen/peerturn/internal/fault"
	"github.com/temirlankasen/peerturn/internal/grant"
	"github.com/temirlankasen/peerturn/internal/identity"
	"github.com/temirlankasen/peerturn/internal/localipc"
	"github.com/temirlankasen/peerturn/internal/protocol"
	"github.com/temirlankasen/peerturn/internal/relayclient"
	"github.com/temirlankasen/peerturn/internal/store"
)

func (d *Daemon) outgoing(p *peerState) (grant.Grant, identity.SessionCertificate, error) {
	cert, err := identity.VerifyChain(p.Chain, identity.RootFingerprint(p.Chain.RootPublicKey), unixNow())
	if err != nil {
		return grant.Grant{}, cert, err
	}
	if p.Revoked {
		return grant.Grant{}, cert, fault.New(fault.Revoked)
	}
	g, err := grant.Verify(p.Outgoing, cert, d.cert, 1, unixNow())
	return g, cert, err
}

func (d *Daemon) makePacket(id string, p *peerState, kind protocol.Kind, body string, parent *store.InboxEntry) (reservedPacket, error) {
	if p.SequenceBlocked && kind != protocol.Receipt {
		return reservedPacket{}, localipc.NewError("binding_error", "needs_rebind")
	}
	g, cert, err := d.outgoing(p)
	if err != nil {
		return reservedPacket{}, err
	}
	now := unixNow()
	messageID := randomID()
	m := protocol.MessagePayload{MessageID: messageID, FromSession: d.cert.SessionID, FromSessionEpoch: d.cert.SessionEpoch, ToSession: cert.SessionID, ToSessionEpoch: cert.SessionEpoch, SenderSeq: d.state.Sequences[id] + 1, GrantID: g.GrantID, GrantEpoch: g.GrantEpoch, GrantHash: grant.Hash(p.Outgoing), Kind: kind, BodyUTF8: body, CreatedAt: now, ExpiresAt: min(now+3600, g.ExpiresAt), ConversationID: randomID(), BudgetID: randomID()}
	if parent != nil {
		m.ConversationID = parent.ConversationID
		m.ParentID = parent.MessageID
		m.ReplyTo = parent.MessageID
		m.BudgetID = parent.BudgetID
		m.Hop = parent.Hop + 1
	}
	h := protocol.RoutingHeader{WireVersion: 1, Suite: protocol.Suite, DstMailbox: routeMailbox(p.RoutingKey), RoutePermitID: routePermit(d.routingPublic()), MessageID: messageID, RecipientKeyEpoch: cert.KeyEpoch, ExpiresAt: m.ExpiresAt}
	key, err := ecdh.X25519().NewPublicKey(cert.EncryptionKey)
	if err != nil {
		return reservedPacket{}, err
	}
	packet, err := envelope.Seal(h, m, d.signingKey(), key)
	if err != nil {
		return reservedPacket{}, err
	}
	return reservedPacket{PeerID: id, Message: m, Packet: packet}, nil
}

func (d *Daemon) send(ctx context.Context, r localipc.Request) (any, error) {
	if err := d.refreshPeerRevocations(ctx); err != nil {
		return nil, err
	}
	if d.state.Reserved != nil {
		if err := d.recoverReserved(ctx); err != nil {
			return nil, err
		}
	}
	if err := d.maintain(ctx); err != nil {
		return nil, err
	}
	id := r.Peer
	p := d.state.Peers[id]
	var parent *store.InboxEntry
	var kind protocol.Kind
	if r.Method == "reply" {
		entry, err := d.db.InboxEntry(ctx, r.MessageID)
		if err != nil {
			return nil, err
		}
		parent = &entry
		id, p = "", nil
		for key, peer := range d.state.Peers {
			g, err := d.incomingGrant(peer)
			if err == nil && g.GrantID == parent.GrantID {
				id = key
				p = peer
				break
			}
		}
		kind = protocol.Reply
	} else {
		switch r.Kind {
		case "coauthor":
			kind = protocol.Coauthor
		case "inform":
			kind = protocol.Inform
		case "ask":
			kind = protocol.Ask
		default:
			return nil, fault.New(fault.ScopeDenied)
		}
	}
	if p == nil {
		return nil, fault.New(store.NotFound)
	}
	if p.IncomingRevoked {
		return nil, fault.New(fault.Revoked)
	}
	if !p.HelloSent || !p.HelloReceived {
		return nil, localipc.NewError("waiting_inviter", "waiting_inviter")
	}
	reserved, err := d.makePacket(id, p, kind, r.Body, parent)
	if err != nil {
		return nil, err
	}
	d.state.Reserved = &reserved
	d.state.Sequences[id]++
	if err = d.save(); err != nil {
		return nil, err
	}
	if err = d.recoverReserved(ctx); err != nil {
		return nil, err
	}
	d.signal()
	// Online policy denial can be reported immediately. Network loss leaves durable
	// sender custody and never creates a fresh packet or claims receiver storage.
	if err = d.flush(ctx); err != nil && retryCustody(err) {
		return nil, err
	}
	receipt, err := d.db.Receipt(ctx, reserved.Message.MessageID)
	if err != nil {
		return nil, err
	}
	return receipt, nil
}

func (d *Daemon) incomingGrant(p *peerState) (grant.Grant, error) {
	cert, err := identity.VerifyChain(p.Chain, identity.RootFingerprint(p.Chain.RootPublicKey), unixNow())
	if err != nil {
		return grant.Grant{}, err
	}
	return grant.Verify(p.Incoming, d.cert, cert, 1, unixNow())
}

func (d *Daemon) openOptions(p *peerState) envelope.OpenOptions {
	kinds := []protocol.Kind{protocol.Coauthor, protocol.Reply, protocol.Receipt}
	if d.state.NativeSessionID != "" {
		kinds = append(kinds, protocol.Ask, protocol.Inform)
	} else if d.state.Codex != nil {
		// Native asks get a signed responder_unavailable rejection; admitting
		// custody here never admits a fork or sends text to the live queue.
		kinds = append(kinds, protocol.Ask, protocol.Inform)
	}
	return envelope.OpenOptions{RecipientKey: d.key, RecipientChain: d.state.Chain, RecipientPin: d.cert.IdentityID, SenderChain: p.Chain, SenderPin: identity.RootFingerprint(p.Chain.RootPublicKey), SignedGrant: p.Incoming, CurrentGrantEpoch: 1, Now: unixNow(), ExpectedMailbox: d.mailbox(), ExpectedPermit: routePermit(p.RoutingKey), AllowedKinds: kinds}
}

func (d *Daemon) sendPacket(ctx context.Context, p protocol.EncryptedPacket) error {
	receipt, err := networkCall(ctx, d, func() (relayclient.Receipt, error) { return d.relay.Send(ctx, p) })
	if err != nil {
		// A relay denial is transport evidence, never a signed peer revocation.
		// Expiry becomes terminal only when our own clock confirms it.
		if problem, ok := errors.AsType[*relayclient.Error](err); ok && problem.Code == "expired_message" && p.Header.ExpiresAt <= unixNow() {
			if maintenanceErr := d.maintain(ctx); maintenanceErr != nil {
				return maintenanceErr
			}
		}
		return err
	}
	if receipt.Stage != "relay_stored" || receipt.Mailbox != p.Header.DstMailbox || receipt.MessageID != p.Header.MessageID || receipt.Seq <= 0 {
		return localipc.NewError("unavailable", "pending")
	}
	if err = d.db.MarkRelayed(ctx, p.Header.MessageID, unixNow()); err != nil {
		return err
	}
	delete(d.state.Pending, p.Header.MessageID)
	return d.save()
}

func (d *Daemon) recoverReserved(ctx context.Context) error {
	r := d.state.Reserved
	if r == nil {
		return nil
	}
	if len(r.NativeBatch) > 0 {
		return d.finishNativeBatch(ctx, r)
	}
	if r.RevokeGrant != "" {
		return d.finishRevoke(ctx, r)
	}
	p := d.state.Peers[r.PeerID]
	if p == nil {
		return fault.New(store.InvalidState)
	}
	raw, err := codec.Canonical(r.Packet)
	if err != nil {
		return err
	}
	var commitErr error
	exists, err := d.db.OutboxExists(ctx, r.Packet.Header.MessageID)
	if err != nil {
		return err
	}
	// Existing receipt custody proves its atomic inbox commit even when the
	// grant expired after that commit. Recovery must not reauthorize old work.
	if !exists {
		if r.Inbound == nil {
			_, commitErr = d.db.OutboxEnqueue(ctx, store.OutboxRequest{Packet: raw, Message: r.Message, GrantID: r.Message.GrantID, Now: unixNow()})
		} else {
			commitErr = d.recoverInbound(ctx, r, p, raw)
		}
		exists, err = d.db.OutboxExists(ctx, r.Packet.Header.MessageID)
		if err != nil {
			return err
		}
	}
	if commitErr != nil && !exists {
		if fault.IsCode(commitErr, store.StorageUnavailable) || fault.IsCode(commitErr, store.SealingFailed) || fault.IsCode(commitErr, store.ReceiptUnavailable) {
			return commitErr
		}
	}
	// A duplicate/control/terminal admission may not invoke the receipt builder.
	// Only an actually retained outbox packet spends the reserved channel sequence.
	if !exists {
		d.state.Sequences[r.PeerID]--
		delete(d.state.Pending, r.Packet.Header.MessageID)
	}
	d.state.Reserved = nil
	if err = d.save(); err != nil {
		return err
	}
	return commitErr
}

func (d *Daemon) recoverInbound(ctx context.Context, r *reservedPacket, p *peerState, receipt []byte) error {
	incoming, err := codec.Canonical(r.Inbound)
	if err != nil {
		return d.rejectSlot(ctx, r.MailboxSeq, nil, err)
	}
	g, err := d.incomingGrant(p)
	if err != nil {
		return d.rejectSlot(ctx, r.MailboxSeq, incoming, err)
	}
	_, err = d.db.InboxCommit(ctx, store.InboxRequest{Packet: incoming, Mailbox: d.mailbox(), MailboxSeq: r.MailboxSeq, GrantID: g.GrantID, Open: d.openOptions(p), Receipt: func(payload protocol.ReceiptPayload) ([]byte, error) {
		if payload.MessageID != r.Inbound.Header.MessageID {
			return nil, fault.New(fault.Conflict)
		}
		return receipt, nil
	}})
	if err != nil {
		return d.rejectSlot(ctx, r.MailboxSeq, incoming, err)
	}
	return nil
}
