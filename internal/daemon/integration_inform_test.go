package daemon

import (
	"testing"

	"github.com/temirlankasen/peerturn/internal/localipc"
)

func TestIntegrationCodexInboxRequiresOutputAcknowledgement(t *testing.T) {
	a, b, _ := nativePair(t)
	r := nativeReceipt(t, a, localipc.Request{Method: "send", Peer: a.peers()[0].ID, Kind: "inform", Body: "explicit body"})
	if err := b.Sync(t.Context()); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		page := nativeValue[InboxPage](t, b, localipc.Request{Method: "inbox"})
		if len(page.Messages) != 1 || page.Messages[0].BodyUTF8 != "explicit body" {
			t.Fatal("lost body on repeated unacknowledged read")
		}
		receipt, err := b.db.Receipt(t.Context(), r.MessageID)
		if err != nil || receipt.Stage != "receiver_stored" {
			t.Fatalf("IPC response claimed output: %+v %v", receipt, err)
		}
	}
	for range 2 {
		nativeCall(t, b, localipc.Request{Method: "inbox.delivered", MessageID: r.MessageID})
	}
	receipt, err := b.db.Receipt(t.Context(), r.MessageID)
	if err != nil || receipt.Stage != "delivered" || receipt.Outcome != "delivered" || receipt.EvidenceKind != "inbox_body_returned" {
		t.Fatalf("shared body output ACK: %+v %v", receipt, err)
	}
}

func TestIntegrationCodexBindingCannotAcquireClaudeHelper(t *testing.T) {
	_, b, _ := nativePair(t)
	_, err := b.Dispatch(t.Context(), localipc.Request{Method: "adapter.open", NativeSession: "11111111-1111-4111-8111-111111111111", NativeSocket: "/tmp/claude.sock", RuntimeVersion: "2.1.306", Provider: "gateway"})
	if err == nil || b.claudeBinding != nil {
		t.Fatal("Codex session acquired a second native adapter binding")
	}
}

func TestIntegrationCodexNeverAuthorizedRecoveryUsesFreshAttempt(t *testing.T) {
	a, b, runtime := nativePair(t)
	runtime.failure = "stale"
	r := nativeReceipt(t, a, localipc.Request{Method: "send", Peer: a.peers()[0].ID, Kind: "coauthor", Body: "recover before submit"})
	if err := b.Sync(t.Context()); err == nil {
		t.Fatal("fixture requires unavailable native transport")
	}
	old, err := b.db.DeliveryIntent(t.Context(), r.MessageID, r.MessageID, unixNow())
	if err != nil || !old.Start {
		t.Fatalf("intent fixture: %+v %v", old, err)
	}
	if err = b.db.RecoverInDoubt(t.Context(), unixNow()); err != nil {
		t.Fatal(err)
	}
	runtime.failure = ""
	if err = b.Sync(t.Context()); err != nil {
		t.Fatal(err)
	}
	receipt, err := b.db.Receipt(t.Context(), r.MessageID)
	if err != nil || receipt.Stage != "adapter_accepted" || receipt.AttemptID == old.ID || len(runtime.ids) != 1 {
		t.Fatalf("proven not-sent attempt not replaced once: %+v %v queue=%v", receipt, err, runtime.ids)
	}
}
