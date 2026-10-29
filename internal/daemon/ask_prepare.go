package daemon

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"time"

	"github.com/temirlankasen/peerturn/internal/claude"
	"github.com/temirlankasen/peerturn/internal/localipc"
	"github.com/temirlankasen/peerturn/internal/protocol"
	"github.com/temirlankasen/peerturn/internal/runtimepolicy"
	"github.com/temirlankasen/peerturn/internal/store"
)

func (d *Daemon) prepareClaudeAsk(ctx context.Context, budget askBudgetReservation, item store.InboxEntry,
	native, instance, text, notificationGrant string, options claude.ResponderOptions) {
	options, snapshot, err := qualifyAskOptions(ctx, options, native)
	var run store.AskRun
	var responder *claude.Responder
	if err == nil {
		run, responder, err = d.authorizePreparedAsk(ctx, budget, item, native, instance, notificationGrant, options, snapshot)
	}
	if err != nil {
		d.failPreparedAsk(ctx, budget, item, run, err)
		return
	}
	result, runErr := responder.Run(ctx, claude.AskRequest{NativeSessionID: native, FramedQuestion: text, Snapshot: snapshot})
	d.finishClaudeAsk(ctx, run, item, native, instance, result, runErr)
}

func (d *Daemon) authorizePreparedAsk(ctx context.Context, budget askBudgetReservation, item store.InboxEntry,
	native, instance, notificationGrant string, options claude.ResponderOptions, snapshot *claude.ResponderSnapshot) (store.AskRun, *claude.Responder, error) {
	d.opMu.Lock()
	defer d.opMu.Unlock()
	d.mu.Lock()
	defer d.mu.Unlock()
	var run store.AskRun
	if err := ctx.Err(); err != nil {
		return run, nil, err
	}
	if d.persistenceFailed || d.state.NativeSessionID != native || !d.claudeBinding.Ready(time.Now()) || d.claudeBinding.Instance != instance {
		return run, nil, localipc.NewError("needs_rebind", "needs_rebind")
	}
	profileID, qualified := runtimepolicy.AskProfile(runtimepolicy.Claude, d.claudeBinding.RuntimeVersion)
	if !qualified {
		return run, nil, localipc.NewError("responder_unavailable", "runtime_version_unqualified")
	}
	if err := d.refreshPeerRevocations(ctx); err != nil {
		return run, nil, err
	}
	attempt, err := d.db.DeliveryIntent(ctx, item.MessageID, randomID(), unixNow())
	if err != nil {
		return run, nil, err
	}
	profile := sha256.Sum256([]byte(profileID))
	run, err = d.db.ReserveAsk(ctx, store.AskReservation{RunID: budget.ID, MessageID: item.MessageID, AttemptID: attempt.ID,
		Parent: d.cert.Binding, ParentContext: bindingContext(d.state), Snapshot: snapshot.Evidence(),
		Model: snapshot.Model, Effort: snapshot.Effort, ProfileHash: hex.EncodeToString(profile[:]), BudgetID: item.BudgetID,
		NotificationGrantID: notificationGrant, Now: unixNow()})
	if err != nil {
		return run, nil, err
	}
	if !run.Start {
		return store.AskRun{}, nil, localipc.NewError("conflict", "in_doubt")
	}
	if err = d.db.AuthorizeAsk(ctx, run.RunID, unixNow()); err != nil {
		return run, nil, err
	}
	options.Observer = func(observeCtx context.Context, stage claude.ResponderStage, child string) error {
		return d.observeAsk(observeCtx, run, stage, child, instance)
	}
	options.PrepareFork = func(prepareCtx context.Context, child string) error {
		d.opMu.Lock()
		defer d.opMu.Unlock()
		d.mu.Lock()
		defer d.mu.Unlock()
		if d.persistenceFailed || !d.claudeBinding.Ready(time.Now()) || d.claudeBinding.Instance != instance {
			return localipc.NewError("needs_rebind", "needs_rebind")
		}
		return d.db.RememberAskFork(prepareCtx, run.RunID, []byte(child), unixNow())
	}
	options.FinishFork = func(finishCtx context.Context, child string) error {
		return d.db.ForgetAskFork(finishCtx, run.RunID, []byte(child))
	}
	options.AdmitPeerInput = func(admitCtx context.Context, child string, submit func() error) error {
		return d.admitAskInput(admitCtx, run, instance, child, submit)
	}
	responder, err := claude.NewResponder(options)
	return run, responder, err
}

func (d *Daemon) failPreparedAsk(ctx context.Context, budget askBudgetReservation, item store.InboxEntry, run store.AskRun, cause error) {
	d.opMu.Lock()
	defer d.opMu.Unlock()
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.askActive, budget.ID)
	d.lastError = askFailureReason(cause)
	finishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	var err error
	if run.Start {
		r := protocol.ReceiptPayload{MessageID: run.MessageID, AttemptID: run.AttemptID, Stage: "responder_failed",
			Outcome: "responder_failed", EvidenceKind: "claude_fork_preflight", ReasonCode: askFailureReason(cause), At: unixNow()}
		err = d.advanceAsk(finishCtx, run, r, nil)
	} else {
		reason := askFailureReason(cause)
		if !store.IsResponderRefusal(reason) {
			reason = "runtime_unavailable"
		}
		var receipt protocol.ReceiptPayload
		receipt, err = d.db.RejectPendingDelivery(finishCtx, item.MessageID, reason, unixNow())
		if err == nil {
			receipt.ReasonCode = askPeerFailure(reason)
			err = d.queueClaudeReceipt(finishCtx, receipt)
		}
	}
	if err = errors.Join(err, d.finishAskBudget(budget.ID)); err != nil {
		d.lastError = safeCode(err)
	}
}
