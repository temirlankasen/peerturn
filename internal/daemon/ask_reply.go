package daemon

import (
	"context"
	"errors"
	"time"

	"github.com/temirlankasen/peerturn/internal/claude"
	"github.com/temirlankasen/peerturn/internal/codec"
	"github.com/temirlankasen/peerturn/internal/localipc"
	"github.com/temirlankasen/peerturn/internal/protocol"
	"github.com/temirlankasen/peerturn/internal/store"
)

func (d *Daemon) cancelAskWorkers() {
	for _, cancel := range d.askActive {
		cancel()
	}
}

func (d *Daemon) observeAsk(ctx context.Context, run store.AskRun, stage claude.ResponderStage, native, instance string) error {
	d.opMu.Lock()
	defer d.opMu.Unlock()
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.persistenceFailed {
		return localipc.NewError("store_unavailable", "in_doubt")
	}
	if err := d.refreshPeerRevocations(ctx); err != nil {
		return err
	}
	if !d.claudeBinding.Ready(time.Now()) || d.claudeBinding.Instance != instance {
		return localipc.NewError("needs_rebind", "needs_rebind")
	}
	name := string(stage)
	if stage == claude.OutboundRedacted {
		name = "reply_redacted"
	}
	r := protocol.ReceiptPayload{MessageID: run.MessageID, AttemptID: run.AttemptID, Stage: name,
		EvidenceKind: "claude_fork_event", Outcome: name, At: unixNow()}
	return d.advanceAsk(ctx, run, r, []byte(native))
}

func (d *Daemon) askPeer(ctx context.Context, message string) (string, *peerState, error) {
	grantID, err := d.db.InboxGrant(ctx, message)
	if err != nil {
		return "", nil, err
	}
	for id, peer := range d.state.Peers {
		g, verifyErr := d.incomingGrant(peer)
		if verifyErr == nil && g.GrantID == grantID {
			return id, peer, nil
		}
	}
	return "", nil, localipc.NewError("revoked", "revoked")
}

func (d *Daemon) askNotification(ctx context.Context, r protocol.ReceiptPayload) (*reservedPacket, *store.NativeNotification, error) {
	id, peer, err := d.askPeer(ctx, r.MessageID)
	if err != nil {
		return nil, nil, err
	}
	r.NativeInputID, r.NativeTurnID = "", ""
	if r.ReasonCode != "" {
		r.ReasonCode = askPeerFailure(r.ReasonCode)
	}
	body, err := codec.Canonical(r)
	if err != nil {
		return nil, nil, err
	}
	packet, err := d.makePacket(id, peer, protocol.Receipt, string(body), nil)
	if err != nil {
		if terminalClaudeError(err) {
			return nil, nil, nil
		}
		return nil, nil, err
	}
	wire, err := codec.Canonical(packet.Packet)
	if err != nil {
		return nil, nil, err
	}
	return &packet, &store.NativeNotification{GrantID: packet.Message.GrantID, Packet: wire}, nil
}

func (d *Daemon) advanceAsk(ctx context.Context, run store.AskRun, receipt protocol.ReceiptPayload, native []byte) error {
	// Terminal custody cannot depend on reverse authority, signing, the journal
	// reservation or capacity in the peer's control lane.
	if receipt.Stage == "responder_failed" || receipt.Stage == "cancelled" || receipt.Stage == "in_doubt" {
		if err := d.db.AdvanceAsk(ctx, run.RunID, receipt, native); err != nil {
			return err
		}
	}
	if err := d.recoverReserved(ctx); err != nil {
		return err
	}
	packet, notice, err := d.askNotification(ctx, receipt)
	if err != nil {
		return err
	}
	var notifications []store.NativeNotification
	if packet != nil {
		batch := &reservedPacket{NativeBatch: []reservedPacket{*packet}}
		d.state.Reserved = batch
		d.state.Sequences[packet.PeerID]++
		if err = d.save(); err != nil {
			return err
		}
		notifications = append(notifications, *notice)
	}
	commitErr := d.db.AdvanceAsk(ctx, run.RunID, receipt, native, notifications...)
	if d.state.Reserved != nil {
		if err = d.finishNativeBatch(context.WithoutCancel(ctx), d.state.Reserved); err != nil {
			return err
		}
	}
	if commitErr == nil {
		d.signal()
	}
	return commitErr
}

func (d *Daemon) finishClaudeAsk(ctx context.Context, run store.AskRun, parent store.InboxEntry, nativeParent, instance string, result claude.AskResult, runErr error) {
	d.opMu.Lock()
	defer d.opMu.Unlock()
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.askActive, run.RunID)
	if failure, ok := errors.AsType[*claude.ResponderError](runErr); ok && failure.Code == claude.ForkCleanupFailed {
		d.askCleanupFailed = true
	}
	if d.persistenceFailed {
		return
	}
	finishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	err := d.refreshPeerRevocations(finishCtx)
	if err == nil && (ctx.Err() != nil || d.state.NativeSessionID != nativeParent || !d.claudeBinding.Ready(time.Now()) || d.claudeBinding.Instance != instance) {
		err = localipc.NewError("cancelled", "cancelled")
	}
	if err == nil {
		err = runErr
	}
	if err == nil && result.Answer == "" {
		err = localipc.NewError("responder_failed", "responder_failed")
	}
	if err == nil && result.Answer != "" {
		err = d.commitAskReply(ctx, run, parent, result.Answer)
	}
	if err != nil {
		r := protocol.ReceiptPayload{MessageID: run.MessageID, AttemptID: run.AttemptID, Stage: "responder_failed",
			EvidenceKind: "claude_fork_failure", Outcome: "responder_failed", ReasonCode: askFailureReason(err), At: unixNow()}
		if ctx.Err() != nil || d.state.NativeSessionID != nativeParent {
			r.Stage, r.Outcome = "cancelled", "cancelled"
		}
		if settleErr := d.advanceAsk(finishCtx, run, r, nil); settleErr != nil {
			d.lastError = safeCode(settleErr)
		}
	}
	if err = d.finishAskBudget(run.RunID); err != nil {
		d.lastError = safeCode(err)
	}
}

func (d *Daemon) commitAskReply(ctx context.Context, run store.AskRun, parent store.InboxEntry, answer string) error {
	if err := d.recoverReserved(ctx); err != nil {
		return err
	}
	id, peer, err := d.askPeer(ctx, run.MessageID)
	if err != nil {
		return err
	}
	if peer.IncomingRevoked {
		return localipc.NewError("revoked", "revoked")
	}
	reply, err := d.makePacket(id, peer, protocol.Reply, answer, &parent)
	if err != nil {
		return err
	}
	d.state.Sequences[id]++
	r := protocol.ReceiptPayload{MessageID: run.MessageID, AttemptID: run.AttemptID, Stage: "reply",
		EvidenceKind: "redacted_reply_commit", Outcome: "reply", At: unixNow()}
	noticePacket, notification, err := d.askNotification(ctx, r)
	if err != nil {
		d.state.Sequences[id]--
		return err
	}
	batch := &reservedPacket{NativeBatch: []reservedPacket{reply}}
	var notifications []store.NativeNotification
	if noticePacket != nil {
		batch.NativeBatch = append(batch.NativeBatch, *noticePacket)
		d.state.Sequences[noticePacket.PeerID]++
		notifications = append(notifications, *notification)
	}
	d.state.Reserved = batch
	if err = d.save(); err != nil {
		return err
	}
	wire, err := codec.Canonical(reply.Packet)
	if err != nil {
		return err
	}
	_, commitErr := d.db.CommitAskReply(ctx, run.RunID, store.OutboxRequest{Packet: wire, Message: reply.Message,
		GrantID: reply.Message.GrantID, Now: r.At}, r, notifications...)
	err = d.finishNativeBatch(context.WithoutCancel(ctx), batch)
	if commitErr == nil && err == nil {
		d.signal()
	}
	return errors.Join(commitErr, err)
}
