package daemon

import (
	"context"
	"errors"
	"sort"
	"time"

	"github.com/temirlankasen/peerturn/internal/codec"
	"github.com/temirlankasen/peerturn/internal/fault"
	"github.com/temirlankasen/peerturn/internal/protocol"
	"github.com/temirlankasen/peerturn/internal/relayclient"
	"github.com/temirlankasen/peerturn/internal/store"
)

// Sync retries immutable ciphertext and resumes only from committed M1b custody.
// WSS is a wake hint; this function is the sole transport/custody authority.
func (d *Daemon) Sync(ctx context.Context) error {
	return d.syncWithBudget(ctx, 2*time.Second)
}

// A separate phase budget lets local fixtures exercise custody under disk
// contention without changing the public Sync deadline or skipping the handshake.
func (d *Daemon) syncWithBudget(ctx context.Context, phaseBudget time.Duration) error {
	d.opMu.Lock()
	defer d.opMu.Unlock()
	ctx = context.WithValue(ctx, operationLockKey{}, d)
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.persistenceFailed {
		return fault.New(store.StorageUnavailable)
	}
	if err := d.refreshPeerRevocations(ctx); err != nil {
		return err
	}
	if err := d.recoverReserved(ctx); err != nil {
		return err
	}
	if err := d.maintain(ctx); err != nil {
		return err
	}
	revokeCtx, revokeCancel := context.WithTimeout(ctx, phaseBudget)
	revokeErr := d.syncRevokes(revokeCtx)
	revokeCancel()
	if retryCustody(revokeErr) {
		return revokeErr
	}
	onboardingCtx, onboardingCancel := context.WithTimeout(ctx, phaseBudget)
	onboardingErr := d.syncJoins(onboardingCtx)
	if retryCustody(onboardingErr) {
		onboardingCancel()
		return onboardingErr
	}
	inviteErr := d.syncInvites(onboardingCtx)
	onboardingCancel()
	if retryCustody(inviteErr) {
		return inviteErr
	}
	if !d.state.Enrolled {
		return nil
	}
	if err := d.syncLinkUpdates(ctx); err != nil {
		return err
	}
	if err := d.syncPresence(ctx); err != nil {
		return err
	}
	flushErr := d.flush(ctx)
	if retryCustody(flushErr) {
		return flushErr
	}
	frontier, err := d.db.AckFrontier(ctx, d.mailbox())
	if err != nil {
		return err
	}
	after, err := wireFrontier(frontier)
	if err != nil {
		return err
	}
	messages, err := networkCall(ctx, d, func() ([]relayclient.Message, error) {
		return d.relay.Fetch(ctx, d.mailbox(), relayclient.FetchOptions{After: after, Limit: 100})
	})
	if err != nil {
		return err
	}
	for _, message := range messages {
		if message.Seq <= 0 || message.Seq > protocol.MaxSafeInteger {
			return fault.New(fault.InvalidEncoding)
		}
		if message.Tombstone {
			if _, err = d.db.CommitTombstone(ctx, d.mailbox(), uint64(message.Seq), unixNow()); err != nil {
				return err
			}
			continue
		}
		if message.Packet == nil {
			return fault.New(fault.InvalidEncoding)
		}
		if err = d.receive(ctx, *message.Packet, uint64(message.Seq)); err != nil {
			return err
		}
	}
	frontier, err = d.db.AckFrontier(ctx, d.mailbox())
	if err != nil {
		return err
	}
	if frontier > 0 {
		through, boundErr := wireFrontier(frontier)
		if boundErr != nil {
			return boundErr
		}
		if through > d.ackedThrough {
			if err = networkDo(ctx, d, func() error { return d.relay.Ack(ctx, d.mailbox(), through) }); err != nil {
				return err
			}
			d.ackedThrough = through
		}
	}
	nativeErr := d.deliverNative(ctx)
	if retryCustody(nativeErr) {
		return errors.Join(flushErr, nativeErr)
	}
	responderErr := d.rejectUnavailableResponders(ctx)
	return errors.Join(flushErr, nativeErr, responderErr, d.flush(ctx))
}

func (d *Daemon) flush(ctx context.Context) error {
	ids := make([]string, 0, len(d.state.Pending))
	for id := range d.state.Pending {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		a, b := d.state.Pending[ids[i]], d.state.Pending[ids[j]]
		if a.PeerID != b.PeerID {
			return a.PeerID < b.PeerID
		}
		return a.SenderSeq < b.SenderSeq
	})
	sent := 0
	var result error
	blocked := map[string]bool{}
	for _, id := range ids {
		peerID := d.state.Pending[id].PeerID
		if blocked[peerID] {
			continue
		}
		peer := d.state.Peers[peerID]
		if peer == nil {
			continue
		}
		raw, err := d.db.OutboxPacket(ctx, id)
		if fault.IsCode(err, store.NotFound) {
			delete(d.state.Pending, id)
			if err = d.save(); err != nil {
				return err
			}
			continue
		}
		if err != nil {
			return err
		}
		if peer.IncomingRevoked {
			control, laneErr := d.db.OutboxIsControl(ctx, id)
			if laneErr != nil {
				return laneErr
			}
			// The independent outgoing grant can retain old data custody after
			// local revoke. Do not submit it or treat it as a failed receipt.
			if !control {
				continue
			}
			if peer.Revoked || peer.NotificationsUnavailable {
				if err = d.stopRevokedReceipt(ctx, id, peer); err != nil {
					return err
				}
				continue
			}
		}
		if peer.SequenceBlocked && !peer.IncomingRevoked || peer.Revoked {
			continue
		}
		p, decodeErr := protocol.DecodePacket(raw)
		if decodeErr != nil {
			return decodeErr
		}
		if err = d.sendPacket(ctx, p); err != nil {
			if retryCustody(err) {
				return err
			}
			if peer.IncomingRevoked && revokedRouteDenial(err) {
				if err = d.stopRevokedReceipt(ctx, id, peer); err != nil {
					return err
				}
				continue
			}
			blocked[peerID] = true
			if result == nil {
				result = err
			}
		}
		sent++
		if sent >= 100 {
			d.signal()
			break
		}
	}
	return result
}

func (d *Daemon) receive(ctx context.Context, packet protocol.EncryptedPacket, seq uint64) error {
	raw, err := codec.Canonical(packet)
	if err != nil {
		return d.rejectSlot(ctx, seq, nil, err)
	}
	index, err := peerRouteIndex(d.state)
	if err != nil {
		return d.rejectSlot(ctx, seq, raw, err)
	}
	id := index[packet.Header.RoutePermitID]
	peer := d.state.Peers[id]
	if peer == nil {
		return d.rejectSlot(ctx, seq, raw, fault.New(fault.NeedsVerify))
	}
	var verified protocol.MessagePayload
	handled, err := d.acceptHandshake(ctx, peer, packet, seq, &verified)
	if handled || err != nil {
		if err == nil {
			return nil
		}
		return d.rejectSlot(ctx, seq, raw, err)
	}
	signed := peer.incomingFor(verified.GrantHash)
	g, err := d.incomingGrantFor(peer, signed)
	if err != nil {
		return d.rejectSlot(ctx, seq, raw, err)
	}
	if verified.Kind == protocol.Receipt {
		_, commitErr := d.db.InboxCommit(ctx, store.InboxRequest{Packet: raw, Mailbox: d.mailbox(), MailboxSeq: seq, GrantID: g.GrantID, Open: d.openOptionsFor(peer, signed)})
		if commitErr != nil {
			return d.rejectSlot(ctx, seq, raw, commitErr)
		}
		if seenErr := d.markSeen(peer); seenErr != nil {
			return seenErr
		}
		if recheckErr := d.recheckParked(ctx, peer); recheckErr != nil {
			return recheckErr
		}
		if renewErr := d.renewLink(ctx, peer, signed); renewErr != nil {
			return renewErr
		}
		return d.refreshPeerRevocations(ctx)
	}
	// The builder is invoked only after admission passes. Its journal reservation
	// makes the exact receipt recoverable if custody commits before a crash.
	var buildErr error
	_, err = d.db.InboxCommit(ctx, store.InboxRequest{Packet: raw, Mailbox: d.mailbox(), MailboxSeq: seq, GrantID: g.GrantID, Open: d.openOptionsFor(peer, signed), Receipt: func(receipt protocol.ReceiptPayload) ([]byte, error) {
		body, encodeErr := codec.Canonical(receipt)
		if encodeErr != nil {
			buildErr = encodeErr
			return nil, encodeErr
		}
		reserved, prepareErr := d.makePacket(id, peer, protocol.Receipt, string(body), nil)
		if prepareErr != nil {
			buildErr = prepareErr
			return nil, prepareErr
		}
		wire, encodeErr := codec.Canonical(reserved.Packet)
		if encodeErr != nil {
			buildErr = encodeErr
			return nil, encodeErr
		}
		reserved.Inbound, reserved.MailboxSeq, reserved.InboundGrant = &packet, seq, signed
		d.state.Reserved = &reserved
		d.state.Sequences[id]++
		buildErr = d.save()
		return wire, buildErr
	}})
	// Only an admitted packet is use of the link that renews its grant.
	admitted := err == nil
	if admitted {
		d.logger().Info("message admitted", "peer", shortID(id), "message", verified.MessageID, "kind", verified.Kind, "seq", verified.SenderSeq)
		if seenErr := d.markSeen(peer); seenErr != nil {
			return seenErr
		}
	}
	if err != nil {
		if buildErr != nil {
			err = buildErr
		}
		if retryCustody(err) || d.persistenceFailed {
			return err
		}
		if rejectErr := d.rejectSlot(ctx, seq, raw, err); rejectErr != nil {
			return rejectErr
		}
	}
	if d.state.Reserved != nil {
		if err = d.recoverReserved(ctx); err != nil {
			return d.rejectSlot(ctx, seq, raw, err)
		}
	}
	if err = d.recheckParked(ctx, peer); err != nil {
		return err
	}
	if admitted {
		if err = d.renewLink(ctx, peer, signed); err != nil {
			return err
		}
	}
	return d.refreshPeerRevocations(ctx)
}

func (d *Daemon) rejectSlot(ctx context.Context, seq uint64, raw []byte, cause error) error {
	if retryCustody(cause) {
		return cause
	}
	d.logger().Warn("packet rejected", "seq", seq, "code", safeCode(cause))
	if raw == nil {
		_, err := d.db.CommitTombstone(ctx, d.mailbox(), seq, unixNow())
		return err
	}
	_, err := d.db.RejectMailbox(ctx, d.mailbox(), seq, raw, fault.Code(safeCode(cause)), unixNow())
	return err
}

func retryCustody(err error) bool {
	for _, code := range []fault.Code{store.StorageUnavailable, store.SealingFailed, store.ReceiptUnavailable, store.Backpressure, fault.SequenceGap} {
		if fault.IsCode(err, code) {
			return true
		}
	}
	return false
}

func wireFrontier(frontier uint64) (int64, error) {
	if frontier > protocol.MaxSafeInteger {
		return 0, fault.New(fault.InvalidEncoding)
	}
	return int64(frontier), nil
}
