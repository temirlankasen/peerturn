package daemon

import (
	"context"

	"github.com/temirlankasen/peerturn/internal/codec"
	"github.com/temirlankasen/peerturn/internal/envelope"
	"github.com/temirlankasen/peerturn/internal/fault"
	"github.com/temirlankasen/peerturn/internal/protocol"
	"github.com/temirlankasen/peerturn/internal/store"
)

// Only a Claude-bound receiver may qualify a fork. Other adapters retain the
// signed refusal path and never dispatch asks into their live turn queue.
func (d *Daemon) rejectUnavailableResponders(ctx context.Context) error {
	items, err := d.db.PendingRoute(ctx, envelope.Responder, 100)
	if err != nil {
		return err
	}
	blocked := map[string]bool{}
	for _, item := range items {
		if blocked[item.GrantID] {
			continue
		}
		if d.state.Codex == nil && d.state.NativeSessionID != "" {
			err = d.startClaudeAsk(ctx, item)
			if err == nil && len(d.askActive) != 0 {
				return nil
			}
		} else {
			err = d.rejectUnavailableResponder(ctx, item)
		}
		if err != nil {
			// A full reciprocal control lane must not prevent other peers'
			// delivery or the flush that can drain it. Periodic sync retries
			// pending asks without a wake loop or a new paid attempt.
			if fault.IsCode(err, store.Backpressure) {
				blocked[item.GrantID] = true
				continue
			}
			return err
		}
	}
	return nil
}

func (d *Daemon) rejectUnavailableResponder(ctx context.Context, item store.InboxEntry) error {
	return d.rejectResponderReason(ctx, item, "protected_path_policy_failed")
}

func (d *Daemon) rejectResponderReason(ctx context.Context, item store.InboxEntry, reason string) error {
	if !store.IsResponderRefusal(reason) {
		reason = "protected_path_policy_failed"
	}
	if d.state.Reserved != nil {
		if err := d.recoverReserved(ctx); err != nil {
			return err
		}
	}
	r := protocol.ReceiptPayload{MessageID: item.MessageID, AttemptID: item.MessageID, Stage: "rejected",
		EvidenceKind: "responder_preflight", ReasonCode: reason, Outcome: "responder_unavailable", At: unixNow()}
	var notifications []store.NativeNotification
	batch := &reservedPacket{}
	for id, peer := range d.state.Peers {
		g, verifyErr := d.incomingGrant(peer)
		if verifyErr != nil || g.GrantID != item.GrantID {
			continue
		}
		peerReceipt := r
		peerReceipt.ReasonCode = askPeerFailure(reason)
		body, err := codec.Canonical(peerReceipt)
		if err != nil {
			return err
		}
		prepared, prepareErr := d.makePacket(id, peer, protocol.Receipt, string(body), nil)
		if prepareErr != nil {
			if terminalClaudeError(prepareErr) {
				break
			}
			return prepareErr
		}
		wire, err := codec.Canonical(prepared.Packet)
		if err != nil {
			return err
		}
		notifications = append(notifications, store.NativeNotification{GrantID: prepared.Message.GrantID, Packet: wire})
		batch.NativeBatch = append(batch.NativeBatch, prepared)
		break
	}
	if len(batch.NativeBatch) > 0 {
		d.state.Reserved = batch
		d.state.Sequences[batch.NativeBatch[0].PeerID]++
		if err := d.save(); err != nil {
			return err
		}
	}
	// The signed notification and local terminal decision share one DB commit.
	// A lost response cannot strand the decision between two custody operations.
	_, commitErr := d.db.RejectPendingDelivery(ctx, item.MessageID, r.ReasonCode, r.At, notifications...)
	if len(batch.NativeBatch) > 0 {
		if err := d.finishNativeBatch(context.WithoutCancel(ctx), batch); err != nil {
			return err
		}
	}
	if commitErr == nil {
		d.signal()
	}
	return commitErr
}
