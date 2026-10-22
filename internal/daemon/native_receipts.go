package daemon

import (
	"context"

	"github.com/temirlankasen/peerturn/internal/codec"
	"github.com/temirlankasen/peerturn/internal/grant"
	"github.com/temirlankasen/peerturn/internal/localipc"
	"github.com/temirlankasen/peerturn/internal/protocol"
	"github.com/temirlankasen/peerturn/internal/store"
)

func (d *Daemon) commitInform(ctx context.Context, ids []string, stage, turn string) error {
	if d.state.Reserved != nil {
		return localipc.NewError("backpressure", "pending")
	}
	grants, err := d.db.ListGrants(ctx)
	if err != nil {
		return err
	}
	// One journal save and one DB transaction cover the bounded set. Recovery
	// only publishes receipts whose outbox insertion committed with the stage.
	batch := &reservedPacket{}
	journaled := false
	defer func() {
		if !journaled {
			for _, packet := range batch.NativeBatch {
				d.state.Sequences[packet.PeerID]--
			}
		}
	}()
	var results []store.InformResult
	for _, id := range ids {
		r := protocol.ReceiptPayload{MessageID: id, AttemptID: id, Stage: stage, Outcome: stage, EvidenceKind: "hook_notice", NativeTurnID: turn, At: unixNow()}
		if stage == "delivered" {
			r.EvidenceKind = "inbox_body_returned"
		}
		entry, entryErr := d.db.InboxEntry(ctx, id)
		if entryErr != nil {
			if terminalClaudeError(entryErr) || safeCode(entryErr) == "not_found" {
				continue
			}
			return entryErr
		}
		previous, receiptErr := d.db.Receipt(ctx, id)
		if receiptErr != nil {
			return receiptErr
		}
		if previous.Stage == "delivered" || previous.Stage == stage {
			continue
		}
		if entry.State == "offered" {
			r.AttemptID = previous.AttemptID
		}
		result := store.InformResult{Receipt: r}
		for peerID, p := range d.state.Peers {
			matched := false
			for _, g := range grants {
				if g.GrantID == entry.GrantID && g.Hash == grant.Hash(p.Incoming) {
					matched = true
					break
				}
			}
			if !matched {
				continue
			}
			peerReceipt := r
			peerReceipt.NativeInputID, peerReceipt.NativeTurnID = "", ""
			body, encodeErr := codec.Canonical(peerReceipt)
			if encodeErr != nil {
				return encodeErr
			}
			prepared, prepareErr := d.makePacket(peerID, p, protocol.Receipt, string(body), nil)
			if prepareErr != nil {
				break
			} // Independent return authority may be unavailable.
			wire, encodeErr := codec.Canonical(prepared.Packet)
			if encodeErr != nil {
				return encodeErr
			}
			result.Notification = &store.NativeNotification{GrantID: prepared.Message.GrantID, Packet: wire}
			batch.NativeBatch = append(batch.NativeBatch, prepared)
			d.state.Sequences[peerID]++
			break
		}
		results = append(results, result)
	}
	if len(batch.NativeBatch) > 0 {
		d.state.Reserved = batch
		journaled = true
		if err = d.save(); err != nil {
			return err
		}
	}
	commitErr := d.db.InformResults(ctx, results)
	if d.state.Reserved != nil {
		// A cancelled request cannot prevent cleanup of its local reservation.
		if err = d.finishNativeBatch(context.WithoutCancel(ctx), batch); err != nil {
			return err
		}
	}
	d.signal()
	return commitErr
}

func (d *Daemon) finishNativeBatch(ctx context.Context, r *reservedPacket) error {
	return d.recoverNativeBatch(ctx, r, d.db.OutboxExists)
}

func (d *Daemon) recoverNativeBatch(ctx context.Context, r *reservedPacket, lookup func(context.Context, string) (bool, error)) error {
	committed := make([]bool, len(r.NativeBatch))
	for i, packet := range r.NativeBatch {
		exists, err := lookup(ctx, packet.Packet.Header.MessageID)
		if err != nil {
			return err
		}
		committed[i] = exists
	}
	// A lookup failure must leave the complete reservation and sequence counters
	// untouched, so recovery can retry without spending a sequence twice.
	for i, packet := range r.NativeBatch {
		if committed[i] {
			d.state.Pending[packet.Packet.Header.MessageID] = outboundOrder{PeerID: packet.PeerID, SenderSeq: packet.Message.SenderSeq}
		} else {
			d.state.Sequences[packet.PeerID]--
		}
	}
	d.state.Reserved = nil
	return d.save()
}

func (d *Daemon) dropNativeReservation(ctx context.Context) error {
	if r := d.state.Reserved; r != nil {
		exists, err := d.db.OutboxExists(context.WithoutCancel(ctx), r.Packet.Header.MessageID)
		if err != nil {
			return err
		}
		if exists {
			return d.recoverReserved(context.WithoutCancel(ctx))
		}
		d.state.Sequences[r.PeerID]--
		d.state.Reserved = nil
		return d.save()
	}
	return nil
}
