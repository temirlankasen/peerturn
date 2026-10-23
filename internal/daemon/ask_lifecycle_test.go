package daemon

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/temirlankasen/peerturn/internal/claude"
	"github.com/temirlankasen/peerturn/internal/codec"
	"github.com/temirlankasen/peerturn/internal/envelope"
	"github.com/temirlankasen/peerturn/internal/fault"
	"github.com/temirlankasen/peerturn/internal/localipc"
	"github.com/temirlankasen/peerturn/internal/protocol"
	"github.com/temirlankasen/peerturn/internal/store"
)

func askLifecycleFixture(t *testing.T) (*Daemon, *Daemon, store.AskRun, store.InboxEntry, string) {
	t.Helper()
	a, b := pairedStores(t)
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	backend := &memoryKeyStore{}
	if err := MigrateAuthoritySecrets(t.Context(), a.options.StateDir, backend); err != nil {
		t.Fatal(err)
	}
	options := a.options
	options.KeyStore = backend
	a, err := Open(t.Context(), options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if closeErr := a.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	})
	instance := bindClaude(t, a)
	packet, err := b.makePacket(peerID(a.state.Chain), b.state.Peers[peerID(a.state.Chain)], protocol.Ask, "shareable fact", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = a.receive(t.Context(), packet.Packet, 1); err != nil {
		t.Fatal(err)
	}
	items, err := a.db.PendingRoute(t.Context(), envelope.Responder, 1)
	if err != nil || len(items) != 1 {
		t.Fatal(items, err)
	}
	item := items[0]
	peer, err := a.claudeSender(item)
	if err != nil {
		t.Fatal(err)
	}
	budget, err := a.reserveAskBudget(peer, a.state.NativeSessionID, item.ConversationID, item.MessageID, unixNow())
	if err != nil {
		t.Fatal(err)
	}
	attempt, err := a.db.DeliveryIntent(t.Context(), item.MessageID, randomID(), unixNow())
	if err != nil {
		t.Fatal(err)
	}
	run, err := a.db.ReserveAsk(t.Context(), store.AskReservation{RunID: budget.ID, MessageID: item.MessageID,
		AttemptID: attempt.ID, Parent: a.cert.Binding, ParentContext: bindingContext(a.state), Snapshot: []byte("local-fixture"),
		Model: "fixture-model", Effort: "high", ProfileHash: "fixture-profile", BudgetID: item.BudgetID, Now: unixNow()})
	if err != nil || !run.Start {
		t.Fatal(run, err)
	}
	if err = a.db.AuthorizeAsk(t.Context(), run.RunID, unixNow()); err != nil {
		t.Fatal(err)
	}
	return a, b, run, item, instance
}

func TestAskDaemonObserveAdmitAndCommit(t *testing.T) {
	parallelDaemonTest(t)
	a, _, run, parent, instance := askLifecycleFixture(t)
	native := "22222222-2222-4222-8222-222222222222"
	if err := a.observeAsk(t.Context(), run, claude.ResponderStarted, native, instance); err != nil {
		t.Fatal(err)
	}
	submitted := 0
	if err := a.admitAskInput(t.Context(), run, instance, native, func() error { submitted++; return nil }); err != nil || submitted != 1 {
		t.Fatal("trusted input was not admitted", err)
	}
	for _, stage := range []claude.ResponderStage{claude.ResponderModelInput, claude.ResponderCompleted, claude.OutboundRedacted} {
		if err := a.observeAsk(t.Context(), run, stage, native, instance); err != nil {
			t.Fatal(err)
		}
	}
	if err := a.commitAskReply(t.Context(), run, parent, "filtered shareable answer"); err != nil {
		t.Fatal(err)
	}
	if err := a.commitAskReply(t.Context(), run, parent, "duplicate"); !fault.IsCode(err, fault.Conflict) {
		t.Fatal("reply was committed twice", err)
	}
	receipt, err := a.db.Receipt(t.Context(), run.MessageID)
	if err != nil || receipt.Stage != "reply" {
		t.Fatal(receipt, err)
	}
}

func TestAskDaemonAdmitRejectsLostAuthorityAndRebind(t *testing.T) {
	parallelDaemonTest(t)
	for _, change := range []string{"key", "rebind", "cancel", "revoke"} {
		t.Run(change, func(t *testing.T) {
			a, b, run, _, instance := askLifecycleFixture(t)
			ctx := t.Context()
			switch change {
			case "key":
				backend, ok := a.authorityKeyStore.(*memoryKeyStore)
				if !ok {
					t.Fatal("missing fixture backend")
				}
				backend.unavailable = true
			case "rebind":
				a.claudeBinding.Instance = randomID()
			case "cancel":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			case "revoke":
				a.state.Peers[peerID(b.state.Chain)].IncomingRevoked = true
				g, err := a.incomingGrant(a.state.Peers[peerID(b.state.Chain)])
				if err != nil {
					t.Fatal(err)
				}
				if err = a.db.GrantRevokeLocal(ctx, g.GrantID, g.GrantEpoch, unixNow()); err != nil {
					t.Fatal(err)
				}
			}
			called := false
			if err := a.admitAskInput(ctx, run, instance, "fork", func() error { called = true; return nil }); err == nil || called {
				t.Fatal("untrusted peer input submitted", err)
			}
		})
	}
}

func TestAskDaemonFinishSettlesWhenNoticeCannotBeCreated(t *testing.T) {
	parallelDaemonTest(t)
	a, b, run, parent, instance := askLifecycleFixture(t)
	a.state.Peers[peerID(b.state.Chain)].Revoked = true
	a.finishClaudeAsk(t.Context(), run, parent, a.state.NativeSessionID, instance, claude.AskResult{},
		&claude.ResponderError{Code: claude.ProviderMismatch})
	receipt, err := a.db.Receipt(t.Context(), run.MessageID)
	if err != nil || receipt.Stage != "responder_failed" || receipt.ReasonCode != "provider_mismatch" {
		t.Fatal("notice authority prevented terminal custody", receipt, err)
	}
	if a.state.AskBudget.Runs[run.RunID].Active {
		t.Fatal("terminal run retained paid capacity")
	}
}

func TestAskDaemonFailureNoticeHasOnlyCoarseReason(t *testing.T) {
	parallelDaemonTest(t)
	a, _, run, _, _ := askLifecycleFixture(t)
	for _, reason := range []string{"needs_rebind", "provider_mismatch", "profile_mismatch", "revoked", "budget_exceeded", "runtime_timeout"} {
		r := protocol.ReceiptPayload{MessageID: run.MessageID, AttemptID: run.AttemptID, Stage: "responder_failed",
			ReasonCode: reason, EvidenceKind: "fixture", At: unixNow()}
		packet, _, err := a.askNotification(t.Context(), r)
		if err != nil || packet == nil {
			t.Fatal(err)
		}
		var got protocol.ReceiptPayload
		if err = codec.Decode([]byte(packet.Message.BodyUTF8), 4096, &got); err != nil {
			t.Fatal(err)
		}
		if got.ReasonCode != "unavailable" && got.ReasonCode != "busy" && got.ReasonCode != "failed" {
			t.Fatal("receiver configuration leaked to peer", got.ReasonCode)
		}
	}
	if got := askFailureReason(fault.New(fault.BudgetExceeded)); got != "budget_exceeded" {
		t.Fatal("store budget error mislabeled", got)
	}
	if got := askFailureReason(errors.New("private diagnostic")); got != "responder_failed" {
		t.Fatal("unbounded diagnostic exposed", got)
	}
}

func TestAskDaemonStartChecksBudgetBeforeFileAuthority(t *testing.T) {
	parallelDaemonTest(t)
	a, b := pairedStores(t)
	bindClaude(t, a)
	p, err := b.makePacket(peerID(a.state.Chain), b.state.Peers[peerID(a.state.Chain)], protocol.Ask, "question", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = a.receive(t.Context(), p.Packet, 1); err != nil {
		t.Fatal(err)
	}
	items, err := a.db.PendingRoute(t.Context(), envelope.Responder, 1)
	if err != nil || len(items) != 1 {
		t.Fatal(items, err)
	}
	if _, err = a.reserveAskBudget("busy", a.state.NativeSessionID, "other", "other", unixNow()); err != nil {
		t.Fatal(err)
	}
	if err = a.startClaudeAsk(t.Context(), items[0]); err != nil {
		t.Fatal(err)
	}
	receipt, err := a.db.Receipt(t.Context(), p.Message.MessageID)
	if err != nil || receipt.ReasonCode != "responder_busy" {
		t.Fatal("expensive authority preflight preceded cheap budget", receipt, err)
	}
	if len(a.askActive) != 0 {
		t.Fatal("busy fork spawned")
	}
	if _, err = a.Dispatch(t.Context(), localipc.Request{Method: "status"}); err != nil {
		t.Fatal("busy ask disabled owner controls", err)
	}
}

func TestAskDaemonHomeRootIsRefusedBeforeReservation(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	a, b, run, parent, instance := askLifecycleFixture(t)
	a.finishClaudeAsk(t.Context(), run, parent, a.state.NativeSessionID, instance, claude.AskResult{},
		&claude.ResponderError{Code: claude.ResponderFailed})
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	a.state.ClaudeProject, a.claudeBinding.WorkingDirectory = home, home
	b.state.Sequences[peerID(a.state.Chain)]++
	packet, err := b.makePacket(peerID(a.state.Chain), b.state.Peers[peerID(a.state.Chain)], protocol.Ask, "question", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = a.receive(t.Context(), packet.Packet, 2); err != nil {
		t.Fatal(err)
	}
	items, err := a.db.PendingRoute(t.Context(), envelope.Responder, 1)
	if err != nil || len(items) != 1 {
		t.Fatal(items, err)
	}
	if err = a.startClaudeAsk(t.Context(), items[0]); err != nil {
		t.Fatal(err)
	}
	receipt, err := a.db.Receipt(t.Context(), packet.Message.MessageID)
	if err != nil || receipt.ReasonCode != "project_root_unqualified" {
		t.Fatal("HOME became a fork read root", receipt, err)
	}
	if a.state.AskBudget.DailyUsed != 1 || len(a.askActive) != 0 {
		t.Fatal("HOME refusal spent another fork reservation")
	}
}
