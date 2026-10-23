package daemon

import (
	"context"
	"strings"
	"testing"

	"github.com/temirlankasen/peerturn/internal/adapters/codex"
	"github.com/temirlankasen/peerturn/internal/localipc"
)

func TestReviewInformNoticeRequiresWrittenAcknowledgementAndInboxRead(t *testing.T) {
	parallelDaemonTest(t)
	a, b, _ := nativePair(t)
	body := strings.Repeat("PRIVATE_BODY_MUST_NOT_ENTER_DEVELOPER_CONTEXT", 300)
	r := nativeReceipt(t, a, localipc.Request{Method: "send", Peer: a.peers()[0].ID, Kind: "inform", Body: body})
	if err := b.Sync(t.Context()); err != nil {
		t.Fatal(err)
	}
	offer := nativeValue[map[string]string](t, b, localipc.Request{Method: "codex_inform", MessageID: "owner-turn"})
	if strings.Contains(offer["additional_context"], "PRIVATE_BODY") || len(offer["additional_context"]) > 2048 {
		t.Fatal("peer body entered hook notice or exceeded inline bound")
	}
	if !strings.Contains(offer["additional_context"], "peer_inbox") || offer["count"] != "1" || offer["senders"] == "" {
		t.Fatalf("missing bounded count/sender/tool notice: %v", offer)
	}
	before, err := b.db.Receipt(t.Context(), r.MessageID)
	if err != nil || before.Stage != "receiver_stored" {
		t.Fatalf("prepare claimed offered: %+v %v", before, err)
	}
	nativeCall(t, b, localipc.Request{Method: "codex_inform_ack", MessageID: "owner-turn", Cursor: offer["offer_token"]})
	after, err := b.db.Receipt(t.Context(), r.MessageID)
	if err != nil || after.Stage != "offered" {
		t.Fatalf("notice ACK: %+v %v", after, err)
	}
	if err = b.Sync(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err = a.Sync(t.Context()); err != nil {
		t.Fatal(err)
	}
	remote, err := a.db.Receipt(t.Context(), r.MessageID)
	if err != nil || remote.Stage != "offered" || remote.NativeTurnID != "" || remote.NativeInputID != "" {
		t.Fatalf("private native IDs crossed into peer receipt: %+v %v", remote, err)
	}
	page := nativeValue[InboxPage](t, b, localipc.Request{Method: "inbox"})
	if len(page.Messages) != 1 || page.Messages[0].BodyUTF8 != body {
		t.Fatal("offered body missing from inbox")
	}
	after, err = b.db.Receipt(t.Context(), r.MessageID)
	if err != nil || after.Stage != "offered" {
		t.Fatalf("body delivered before successful output ACK: %+v %v", after, err)
	}
	nativeCall(t, b, localipc.Request{Method: "inbox.delivered", MessageID: r.MessageID})
	after, err = b.db.Receipt(t.Context(), r.MessageID)
	if err != nil || after.Stage != "delivered" {
		t.Fatalf("body read: %+v %v", after, err)
	}
	again := nativeValue[InboxPage](t, b, localipc.Request{Method: "inbox"})
	if len(again.Messages) != 1 || again.Messages[0].BodyUTF8 != body || again.Messages[0].Stage != "delivered" {
		t.Fatal("read body cannot be recovered after a lost response")
	}
	nativeReceipt(t, b, localipc.Request{Method: "reply", Peer: b.peers()[0].ID, MessageID: r.MessageID, Body: "reply after inbox read"})
	for _, p := range b.state.Peers {
		g, grantErr := b.incomingGrant(p)
		if grantErr != nil {
			t.Fatal(grantErr)
		}
		nativeCall(t, b, localipc.Request{Method: "revoke", GrantID: g.GrantID})
	}
	after, err = b.db.Receipt(t.Context(), r.MessageID)
	if err != nil || after.Stage != "delivered" {
		t.Fatalf("revoke invented undelivery: %+v %v", after, err)
	}
}

func nativeValue[T any](t *testing.T, d *Daemon, r localipc.Request) T {
	t.Helper()
	value, ok := nativeCall(t, d, r).(T)
	if !ok {
		t.Fatal("native response type")
	}
	return value
}

func TestReviewRevokedOutgoingGrantDoesNotWedgeNativeItems(t *testing.T) {
	parallelDaemonTest(t)
	a, b, runtime := nativePair(t)
	for _, kind := range []string{"inform", "coauthor"} {
		nativeReceipt(t, a, localipc.Request{Method: "send", Peer: a.peers()[0].ID, Kind: kind, Body: "retained " + kind})
	}
	runtime.failure = "stale"
	if err := b.Sync(t.Context()); err == nil {
		t.Fatal("fixture expected unloaded native thread")
	}
	runtime.failure = ""
	for _, p := range b.state.Peers {
		p.Revoked = true
	}
	if _, err := b.Dispatch(t.Context(), localipc.Request{Method: "codex_inform", Session: b.state.Label, NativeThread: runtime.thread, MessageID: "owner"}); err != nil {
		t.Fatalf("outgoing revoke poisoned incoming inform: %v", err)
	}
	if err := b.Sync(t.Context()); err != nil {
		t.Fatalf("outgoing revoke poisoned native queue: %v", err)
	}
	if len(runtime.ids) != 1 {
		t.Fatalf("live item blocked: %v", runtime.ids)
	}
}

func TestReviewRevokeDeletesAcceptedNativeItemsAndFencesLateStart(t *testing.T) {
	parallelDaemonTest(t)
	a, b, runtime := nativePair(t)
	r := nativeReceipt(t, a, localipc.Request{Method: "send", Peer: a.peers()[0].ID, Kind: "coauthor", Body: "busy queued"})
	if err := b.Sync(t.Context()); err != nil {
		t.Fatal(err)
	}
	for _, p := range b.state.Peers {
		g, err := b.incomingGrant(p)
		if err != nil {
			t.Fatal(err)
		}
		nativeCall(t, b, localipc.Request{Method: "revoke", GrantID: g.GrantID})
		nativeCall(t, b, localipc.Request{Method: "revoke", GrantID: g.GrantID})
	}
	if len(runtime.deleted) != 1 || runtime.deleted[0] != "queue-"+r.MessageID {
		t.Fatalf("native queue not deleted once: %v", runtime.deleted)
	}
	receipt, err := b.db.Receipt(t.Context(), r.MessageID)
	if err != nil || receipt.Stage != "revoked" || receipt.Outcome != "dequeued" {
		t.Fatalf("delete outcome: %+v %v", receipt, err)
	}
	nativeCall(t, b, localipc.Request{Method: "codex_peer_started", MessageID: r.MessageID})
	receipt, err = b.db.Receipt(t.Context(), r.MessageID)
	if err != nil || receipt.Stage != "in_doubt" || receipt.Outcome != "revoked_started" {
		t.Fatalf("revoked start silently delivered: %+v %v", receipt, err)
	}
}

func (f *nativeFixture) Delete(_ context.Context, b codex.Binding, id string) (bool, error) {
	if b.ThreadID != f.thread || b.DaemonID != "test-daemon" || b.Version != codex.SupportedVersion {
		return false, &codex.Error{Code: "needs_rebind"}
	}
	f.deleted = append(f.deleted, id)
	if result, ok := f.deleteResults[id]; ok {
		return result, nil
	}
	return true, nil
}
