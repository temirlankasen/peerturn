package daemon

import (
	"context"

	"github.com/temirlankasen/peerturn/internal/codec"
	"github.com/temirlankasen/peerturn/internal/fault"
	"github.com/temirlankasen/peerturn/internal/grant"
	"github.com/temirlankasen/peerturn/internal/identity"
	"github.com/temirlankasen/peerturn/internal/protocol"
)

func (d *Daemon) revoke(ctx context.Context, grantID string) (any, error) {
	if d.state.Reserved != nil {
		if err := d.recoverReserved(ctx); err != nil && d.state.Reserved != nil {
			return nil, err
		}
	}
	var id string
	var p *peerState
	installed, readErr := d.db.ListGrants(ctx)
	if readErr != nil {
		return nil, readErr
	}
	for _, g := range installed {
		if g.GrantID != grantID || !identity.EqualBinding(g.Issuer, d.cert.Binding) {
			continue
		}
		for peerID, peer := range d.state.Peers {
			if g.Hash == grant.Hash(peer.Incoming) {
				id = peerID
				p = peer
				break
			}
		}
	}
	if p == nil {
		return nil, fault.New(fault.ScopeDenied)
	}
	if !p.IncomingRevoked {
		if err := d.deleteNativeGrant(ctx, grantID); err != nil {
			return nil, err
		}
		receipts, err := d.db.RevokeReceipts(ctx, grantID, unixNow())
		if err != nil {
			return nil, err
		}
		r := &reservedPacket{PeerID: id, RevokeGrant: grantID, Revocations: map[string]protocol.EncryptedPacket{}, Orders: map[string]outboundOrder{}}
		startSeq := d.state.Sequences[id]
		for _, receipt := range receipts {
			body, encodeErr := codec.Canonical(receipt)
			if encodeErr != nil {
				return nil, encodeErr
			}
			prepared, prepareErr := d.makePacket(id, p, protocol.Receipt, string(body), nil)
			if prepareErr != nil {
				if !retryCustody(prepareErr) {
					r.NoNotifications = true
					r.Revocations = map[string]protocol.EncryptedPacket{}
					r.Orders = map[string]outboundOrder{}
					d.state.Sequences[id] = startSeq
					break
				}
				return nil, prepareErr
			}
			r.Revocations[receipt.MessageID] = prepared.Packet
			d.state.Sequences[id]++
			r.Orders[prepared.Packet.Header.MessageID] = outboundOrder{PeerID: id, SenderSeq: prepared.Message.SenderSeq}
		}
		d.state.Reserved = r
		if err = d.save(); err != nil {
			return nil, err
		}
		if err = d.finishRevoke(ctx, r); err != nil {
			return nil, err
		}
	}
	d.signal()
	err := d.syncRevokes(ctx)
	return map[string]any{"grant_id": grantID, "state": "revoked", "relay_pending": err != nil, "notifications_unavailable": p.NotificationsUnavailable}, nil
}

func (d *Daemon) finishRevoke(ctx context.Context, r *reservedPacket) error {
	p := d.state.Peers[r.PeerID]
	if p == nil {
		return invalidHandshake()
	}
	var err error
	if r.NoNotifications {
		err = d.db.GrantRevokeLocal(ctx, r.RevokeGrant, 1, unixNow())
	} else {
		err = d.db.GrantRevoke(ctx, r.RevokeGrant, 1, unixNow(), func(receipt protocol.ReceiptPayload) ([]byte, error) {
			packet, ok := r.Revocations[receipt.MessageID]
			if !ok {
				return nil, fault.New(fault.Conflict)
			}
			// Native acceptance cannot be undone; revocation preserves that uncertainty.
			if receipt.Stage != "revoked" && receipt.Stage != "in_doubt" {
				return nil, fault.New(fault.Conflict)
			}
			return codec.Canonical(packet)
		})
	}
	if err != nil {
		return err
	}
	for _, packet := range r.Revocations {
		exists, existsErr := d.db.OutboxExists(ctx, packet.Header.MessageID)
		if existsErr != nil {
			return existsErr
		}
		if !exists {
			return fault.New(fault.Conflict)
		}
	}
	p.IncomingRevoked = true
	d.cancelAskWorkers()
	p.RevokeRoutePending = true
	p.NotificationsUnavailable = r.NoNotifications
	d.state.Reserved = nil
	return d.save()
}

func (d *Daemon) syncRevokes(ctx context.Context) error {
	for _, p := range d.state.Peers {
		if !p.RevokeRoutePending {
			continue
		}
		if err := networkDo(ctx, d, func() error { return d.relay.RevokePermit(ctx, d.mailbox(), routePermit(p.RoutingKey), 2) }); err != nil {
			return err
		}
		p.RevokeRoutePending = false
		if err := d.save(); err != nil {
			return err
		}
	}
	return nil
}
