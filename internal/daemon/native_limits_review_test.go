package daemon

import (
	"context"
	"strings"
	"testing"

	"github.com/temirlankasen/peerturn/internal/localipc"
	"github.com/temirlankasen/peerturn/internal/protocol"
)

func TestReviewInformReservationCannotBeOverwrittenOrPublishUnreturnedOffer(t *testing.T) {
	a, b, _ := nativePair(t)
	r := nativeReceipt(t, a, localipc.Request{Method: "send", Peer: a.peers()[0].ID, Kind: "inform", Body: "pending"})
	if err := b.Sync(t.Context()); err != nil {
		t.Fatal(err)
	}
	peer := b.peers()[0].ID
	reserved, err := b.makePacket(peer, b.state.Peers[peer], protocol.Coauthor, "earlier reservation", nil)
	if err != nil {
		t.Fatal(err)
	}
	b.state.Reserved = &reserved
	b.state.Sequences[peer]++
	beforeSeq := b.state.Sequences[peer]
	if _, err = b.Dispatch(t.Context(), localipc.Request{Method: "codex_inform", Session: b.state.Label, NativeThread: b.state.Codex.ThreadID, MessageID: "owner"}); err == nil {
		t.Fatal("unrecovered reservation was ignored")
	}
	if b.state.Reserved != &reserved || b.state.Sequences[peer] != beforeSeq {
		t.Fatal("reservation overwritten")
	}
	if err = b.recoverReserved(t.Context()); err != nil {
		t.Fatal(err)
	}
	offer := nativeValue[map[string]string](t, b, localipc.Request{Method: "codex_inform", MessageID: "owner"})
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err = b.Dispatch(ctx, localipc.Request{Method: "codex_inform_ack", Session: b.state.Label, NativeThread: b.state.Codex.ThreadID, MessageID: "owner", Cursor: offer["offer_token"]}); err == nil {
		t.Fatal("cancelled ACK succeeded")
	}
	after, err := b.db.Receipt(t.Context(), r.MessageID)
	if err != nil || after.Stage != "receiver_stored" || b.state.Reserved != nil {
		t.Fatalf("unreturned offer consumed or reserved: %+v %v", after, err)
	}
}

func TestReviewRevokeMixedNativeOutcomesWhileBindingStale(t *testing.T) {
	a, b, runtime := nativePair(t)
	var ids []string
	for _, body := range []string{"queued one", "already starting two"} {
		r := nativeReceipt(t, a, localipc.Request{Method: "send", Peer: a.peers()[0].ID, Kind: "coauthor", Body: body})
		ids = append(ids, r.MessageID)
	}
	if err := b.Sync(t.Context()); err != nil {
		t.Fatal(err)
	}
	runtime.deleteResults = map[string]bool{"queue-" + ids[1]: false}
	runtime.failure = "stale"
	for _, p := range b.state.Peers {
		g, err := b.incomingGrant(p)
		if err != nil {
			t.Fatal(err)
		}
		nativeCall(t, b, localipc.Request{Method: "revoke", GrantID: g.GrantID})
	}
	if len(runtime.deleted) != 2 {
		t.Fatalf("stale binding skipped native deletion: %v", runtime.deleted)
	}
	for i, id := range ids {
		r, err := b.db.Receipt(t.Context(), id)
		wantStage, wantOutcome := "revoked", "dequeued"
		if i == 1 {
			wantStage, wantOutcome = "in_doubt", "already_admitted"
		}
		if err != nil || r.Stage != wantStage || r.Outcome != wantOutcome {
			t.Fatalf("per-item revoke: %+v %v", r, err)
		}
	}
}

func TestReviewDaemonIdentityAndVersionCannotDrift(t *testing.T) {
	_, b, _ := nativePair(t)
	original := *b.state.Codex
	for _, field := range []string{"daemon", "version"} {
		changed := original
		if field == "daemon" {
			changed.DaemonID = "replacement"
		} else {
			changed.Version = "different"
		}
		b.state.Codex = &changed
		if _, err := b.Dispatch(t.Context(), localipc.Request{Method: "codex_inform", Session: b.state.Label, NativeThread: changed.ThreadID, MessageID: "owner"}); err == nil {
			t.Fatalf("%s drift admitted", field)
		}
	}
	b.state.Codex = &original
}

func TestReviewOfferedInformRetainsTrueStageOnRevoke(t *testing.T) {
	a, b, _ := nativePair(t)
	r := nativeReceipt(t, a, localipc.Request{Method: "send", Peer: a.peers()[0].ID, Kind: "inform", Body: "offered only"})
	if err := b.Sync(t.Context()); err != nil {
		t.Fatal(err)
	}
	offer := nativeValue[map[string]string](t, b, localipc.Request{Method: "codex_inform", MessageID: "owner"})
	nativeCall(t, b, localipc.Request{Method: "codex_inform_ack", MessageID: "owner", Cursor: offer["offer_token"]})
	for _, p := range b.state.Peers {
		g, err := b.incomingGrant(p)
		if err != nil {
			t.Fatal(err)
		}
		nativeCall(t, b, localipc.Request{Method: "revoke", GrantID: g.GrantID})
	}
	after, err := b.db.Receipt(t.Context(), r.MessageID)
	if err != nil || after.Stage != "offered" {
		t.Fatalf("revoke erased notice evidence: %+v %v", after, err)
	}
	if strings.Contains(after.EvidenceKind, "revok") {
		t.Fatal("incorrect revoke evidence")
	}
}
