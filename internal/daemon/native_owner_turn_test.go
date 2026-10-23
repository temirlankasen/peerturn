package daemon

import (
	"context"
	"testing"
	"time"

	"github.com/temirlankasen/peerturn/internal/adapters/codex"
	"github.com/temirlankasen/peerturn/internal/localipc"
)

type ownerTurnRuntime struct {
	NativeRuntime
	turn string
}

type ownerTurnBudgetRuntime struct {
	NativeRuntime
	deadline time.Time
}

func (r *ownerTurnBudgetRuntime) ValidateOwnerTurn(ctx context.Context, _ codex.Binding, _ string) error {
	r.deadline, _ = ctx.Deadline()
	return nil
}

func TestCodexOwnerBoundaryAllowsBoundedRolloutReplay(t *testing.T) {
	parallelDaemonTest(t)
	_, b, _ := nativePair(t)
	runtime := &ownerTurnBudgetRuntime{NativeRuntime: b.native}
	b.native = runtime
	ctx, cancel := context.WithTimeout(t.Context(), codex.OwnerTurnTimeout)
	defer cancel()
	deadline, _ := ctx.Deadline()
	if _, err := b.Dispatch(ctx, localipc.Request{Method: "codex_inform", Session: b.state.Label,
		NativeThread: b.state.Codex.ThreadID, MessageID: "owner-turn"}); err != nil {
		t.Fatal(err)
	}
	if runtime.deadline != deadline {
		t.Fatal("owner verification shortened the caller's bounded replay deadline")
	}
}

func (r *ownerTurnRuntime) ValidateOwnerTurn(ctx context.Context, b codex.Binding, turn string) error {
	if err := r.Validate(ctx, b); err != nil {
		return err
	}
	if turn != r.turn {
		return &codex.Error{Code: "needs_rebind"}
	}
	return nil
}

func TestCodexInternalReviewCannotRecordOrAcknowledgeOwnerBoundary(t *testing.T) {
	parallelDaemonTest(t)
	a, b, _ := nativePair(t)
	runtime := &ownerTurnRuntime{NativeRuntime: b.native, turn: "owner-turn"}
	b.native = runtime
	fyi := nativeReceipt(t, a, localipc.Request{Method: "send", Peer: a.peers()[0].ID, Kind: "inform", Body: "pending owner notice"})
	if err := b.Sync(t.Context()); err != nil {
		t.Fatal(err)
	}
	link := syntheticJoinLink("review-title.invalid", "A")
	r := localipc.Request{Method: "codex_inform", Session: b.state.Label, NativeThread: b.state.Codex.ThreadID,
		MessageID: "internal-review-turn", OwnerInvites: []string{link}}
	if _, err := b.Dispatch(t.Context(), r); err == nil || len(b.joinOrigins.owner) != 0 || b.informOffer != nil || b.hookVerified != 0 {
		t.Fatal("internal review acquired an owner invitation or inform offer")
	}
	offer := nativeValue[map[string]string](t, b, localipc.Request{Method: "codex_inform", MessageID: "owner-turn"})
	runtime.turn = "next-owner-turn"
	r.Method, r.MessageID, r.Cursor, r.OwnerInvites = "codex_inform_ack", "owner-turn", offer["offer_token"], nil
	if _, err := b.Dispatch(t.Context(), r); err == nil {
		t.Fatal("stale owner turn acknowledged the inform notice")
	}
	receipt, err := b.db.Receipt(t.Context(), fyi.MessageID)
	if err != nil || receipt.Stage != "receiver_stored" {
		t.Fatal("rejected owner boundary consumed inform")
	}
	runtime.turn = "owner-turn"
	// Revalidating the exact turn for ACK may take longer than the old 3s TTL.
	b.informOffer.at = unixNow() - 4
	nativeCall(t, b, localipc.Request{Method: "codex_inform_ack", MessageID: "owner-turn", Cursor: offer["offer_token"]})
}

func TestCodexOwnerBoundaryFailsClosedWithoutTurnVerifier(t *testing.T) {
	parallelDaemonTest(t)
	_, b, _ := nativePair(t)
	// Embedding only the base interface hides optional runtime capabilities.
	b.native = struct{ NativeRuntime }{b.native}
	_, err := b.Dispatch(t.Context(), localipc.Request{Method: "codex_inform", Session: b.state.Label,
		NativeThread: b.state.Codex.ThreadID, MessageID: "owner-turn", OwnerInvites: []string{syntheticJoinLink("unknown.invalid", "A")}})
	if err == nil || len(b.joinOrigins.owner) != 0 || b.hookVerified != 0 {
		t.Fatal("unverifiable runtime accepted an owner boundary")
	}
}
