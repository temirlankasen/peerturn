package daemon

import (
	"context"
	"testing"

	"github.com/temirlankasen/peerturn/internal/claude"
	"github.com/temirlankasen/peerturn/internal/codec"
	"github.com/temirlankasen/peerturn/internal/envelope"
	"github.com/temirlankasen/peerturn/internal/fault"
	"github.com/temirlankasen/peerturn/internal/protocol"
	"github.com/temirlankasen/peerturn/internal/store"
)

func fillAskControlLane(t *testing.T, a, b *Daemon) {
	t.Helper()
	id := peerID(b.state.Chain)
	for range 129 {
		body, err := codec.Canonical(protocol.ReceiptPayload{MessageID: randomID(), AttemptID: randomID(),
			Stage: "receiver_stored", EvidenceKind: "fixture", At: unixNow()})
		if err != nil {
			t.Fatal(err)
		}
		packet, err := a.makePacket(id, a.state.Peers[id], protocol.Receipt, string(body), nil)
		if err != nil {
			t.Fatal(err)
		}
		wire, err := codec.Canonical(packet.Packet)
		if err != nil {
			t.Fatal(err)
		}
		_, err = a.db.OutboxEnqueue(t.Context(), store.OutboxRequest{Packet: wire, Message: packet.Message,
			GrantID: packet.Message.GrantID, Now: unixNow()})
		if fault.IsCode(err, store.Backpressure) {
			return
		}
		if err != nil {
			t.Fatal(err)
		}
		a.state.Sequences[id]++
	}
	t.Fatal("control lane did not enforce its bound")
}

func TestAskDaemonFinishSettlesUnderControlBackpressure(t *testing.T) {
	a, b, run, parent, instance := askLifecycleFixture(t)
	fillAskControlLane(t, a, b)
	a.finishClaudeAsk(t.Context(), run, parent, a.state.NativeSessionID, instance, claude.AskResult{},
		&claude.ResponderError{Code: claude.ProfileMismatch})
	receipt, err := a.db.Receipt(t.Context(), run.MessageID)
	if err != nil || receipt.Stage != "responder_failed" {
		t.Fatal("full notification lane wedged terminal state", receipt, err)
	}
	if a.state.Reserved != nil || a.state.AskBudget.Runs[run.RunID].Active {
		t.Fatal("terminal failure stranded a journal/budget reservation")
	}
	if uncertain, queryErr := a.db.UncertainDeliveries(t.Context()); queryErr != nil || uncertain {
		t.Fatal("failed ask fenced coauthor", uncertain, queryErr)
	}
}

func TestAskDaemonStartChecksControlCapacityBeforeKeystore(t *testing.T) {
	a, b := pairedStores(t)
	bindClaude(t, a)
	packet, err := b.makePacket(peerID(a.state.Chain), b.state.Peers[peerID(a.state.Chain)], protocol.Ask, "question", nil)
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
	fillAskControlLane(t, a, b)
	if err = a.startClaudeAsk(t.Context(), items[0]); !fault.IsCode(err, store.Backpressure) {
		t.Fatal("full control lane did not refuse preflight", err)
	}
	if a.state.AskBudget != nil || len(a.askActive) != 0 {
		t.Fatal("full control lane consumed a fork reservation")
	}
	item, err := a.db.Inbox(t.Context(), packet.Message.MessageID)
	if err != nil || item.State != "pending" {
		t.Fatal("backpressure changed ask custody", item, err)
	}
}

func TestAskDaemonPreflightRaceClosesIntentDespiteFullLane(t *testing.T) {
	a, b := pairedStores(t)
	bindClaude(t, a)
	packet, err := b.makePacket(peerID(a.state.Chain), b.state.Peers[peerID(a.state.Chain)], protocol.Ask, "question", nil)
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
	budget, err := a.reserveAskBudget("peer", a.state.NativeSessionID, items[0].ConversationID, packet.Message.MessageID, unixNow())
	if err != nil {
		t.Fatal(err)
	}
	attempt, err := a.db.DeliveryIntent(t.Context(), packet.Message.MessageID, randomID(), unixNow())
	if err != nil {
		t.Fatal(err)
	}
	fillAskControlLane(t, a, b)
	a.failPreparedAsk(t.Context(), budget, items[0], store.AskRun{}, fault.New(store.Backpressure))
	receipt, err := a.db.Receipt(t.Context(), packet.Message.MessageID)
	if err != nil || receipt.Stage != "rejected" || receipt.AttemptID != attempt.ID {
		t.Fatal("preflight race stranded intent", receipt, err)
	}
	if a.state.AskBudget.Runs[budget.ID].Active || a.state.Reserved != nil {
		t.Fatal("preflight race stranded capacity or packet reservation")
	}
}

func TestAskDaemonCancelledCleanupFailureClosesFurtherAsk(t *testing.T) {
	a, _, run, parent, instance := askLifecycleFixture(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	a.finishClaudeAsk(ctx, run, parent, a.state.NativeSessionID, instance, claude.AskResult{},
		&claude.ResponderError{Code: claude.ForkCleanupFailed})
	if !a.askCleanupFailed {
		t.Fatal("cancelled cleanup failure left ask admission enabled")
	}
	if _, err := a.captureAskOptions(); askFailureReason(err) != "fork_cleanup_failed" {
		t.Fatal("cleanup fence missing", err)
	}
}
