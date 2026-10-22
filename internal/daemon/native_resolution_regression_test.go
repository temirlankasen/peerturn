package daemon

import (
	"testing"

	"github.com/temirlankasen/peerturn/internal/localipc"
)

func TestCodexNotSentCannotReplayAmbiguousNativeSubmission(t *testing.T) {
	a, b, runtime := nativePair(t)
	runtime.failure = "in_doubt"
	r := nativeReceipt(t, a, localipc.Request{Method: "send", Peer: a.peers()[0].ID, Kind: "coauthor", Body: "may already have started"})
	if err := b.Sync(t.Context()); err == nil {
		t.Fatal("fixture requires ambiguous queue/add")
	}
	before, err := b.db.Receipt(t.Context(), r.MessageID)
	if err != nil || before.Stage != "in_doubt" {
		t.Fatalf("ambiguity fixture: %+v %v", before, err)
	}
	for _, explicitSession := range []bool{false, true} {
		request := localipc.Request{Method: "delivery.resolve", MessageID: r.MessageID, Outcome: "not_sent"}
		if explicitSession {
			request.Session, request.NativeThread = b.state.Label, runtime.thread
		}
		if _, err = b.Dispatch(t.Context(), request); safeCode(err) != "in_doubt" {
			t.Fatalf("no evidence of never-started native item: %v", err)
		}
	}
	runtime.failure = ""
	if err = b.Sync(t.Context()); err != nil {
		t.Fatal(err)
	}
	after, err := b.db.Receipt(t.Context(), r.MessageID)
	if err != nil || after != before || len(runtime.ids) != 1 {
		t.Fatalf("ambiguous submission replayed: %+v %v queues=%v", after, err, runtime.ids)
	}
	// An owner assertion of submission settles uncertainty without replay.
	resolved := nativeReceipt(t, b, localipc.Request{Method: "delivery.resolve", MessageID: r.MessageID, Outcome: "sent"})
	if resolved.Stage != "adapter_accepted" || resolved.AttemptID != before.AttemptID {
		t.Fatalf("sent reconciliation lost exact attempt: %+v", resolved)
	}
}
