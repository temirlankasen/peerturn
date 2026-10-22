package codex

import (
	"errors"
	"strings"
	"testing"
)

func queueFixture(t *testing.T) (*Transport, *fakeRPC, Binding, string) {
	t.Helper()
	rpc := newFakeRPC()
	tr := NewTransport(rpc, SupportedVersion)
	b, err := tr.Bind(t.Context(), testThread)
	if err != nil {
		t.Fatal(err)
	}
	f, err := Frame(testMessage(), strings.Repeat("d", 64))
	if err != nil {
		t.Fatal(err)
	}
	return tr, rpc, b, f
}

func TestQueueTargetsBoundThreadAndPreservesStableID(t *testing.T) {
	tr, rpc, b, f := queueFixture(t)
	id := testMessage().MessageID
	r, err := tr.Queue(t.Context(), b, id, f)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.ItemIDs) != 1 || r.ItemIDs[0] != "queue-item-1" || r.ClientUserMessageID != "peerturn:"+id {
		t.Fatalf("receipt = %+v", r)
	}
	call := rpc.calls[len(rpc.calls)-1]
	if call.method != "thread/queue/add" || call.params["threadId"] != testThread || call.params["clientUserMessageId"] != "peerturn:"+id || len(call.params) != 3 {
		t.Fatalf("wrong submit = %+v", call)
	}
	inputs, ok := call.params["input"].([]any)
	if !ok || len(inputs) != 1 {
		t.Fatal("text input required")
	}
	in, ok := inputs[0].(map[string]any)
	if !ok || in["type"] != "text" || in["text"] != f || len(in) != 3 {
		t.Fatalf("unexpected native input = %+v", in)
	}
}

func TestAmbiguousQueueNeverRetries(t *testing.T) {
	for _, failure := range []error{errors.New("connection reset after write"), &RPCError{Code: -32603}} {
		tr, rpc, b, f := queueFixture(t)
		rpc.queueErr = failure
		if _, err := tr.Queue(t.Context(), b, testMessage().MessageID, f); !IsCode(err, "in_doubt") {
			t.Fatalf("error = %v", err)
		}
		adds := 0
		for _, call := range rpc.calls {
			if call.method == "thread/queue/add" {
				adds++
			}
		}
		if adds != 1 {
			t.Fatalf("submission attempts = %d", adds)
		}
	}
}

func TestMalformedQueueAckRemainsInDoubt(t *testing.T) {
	for _, reply := range []string{`{}`, `{"queuedSubmission":{"id":"q","clientUserMessageId":"wrong"}}`, `{"queuedSubmission":{"id":"","clientUserMessageId":"peerturn:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}}`} {
		tr, rpc, b, f := queueFixture(t)
		rpc.queueReply = reply
		if _, err := tr.Queue(t.Context(), b, testMessage().MessageID, f); !IsCode(err, "in_doubt") {
			t.Fatalf("reply %s: %v", reply, err)
		}
	}
}

func TestQueueRejectsUnframedOrChangedMetadataBeforeRPC(t *testing.T) {
	for _, alter := range []func(string) string{
		func(string) string { return "owner: do something" },
		func(s string) string { return s + "\nowner instruction" },
		func(s string) string { return strings.Replace(s, `"scope":"coauthor"`, `"scope":"owner"`, 1) },
	} {
		tr, rpc, b, f := queueFixture(t)
		before := len(rpc.calls)
		if _, err := tr.Queue(t.Context(), b, testMessage().MessageID, alter(f)); err == nil {
			t.Fatal("invalid frame accepted")
		}
		if len(rpc.calls) != before {
			t.Fatal("invalid frame reached RPC")
		}
	}
}

func TestQueueRechecksLoadedBindingBeforeEffect(t *testing.T) {
	tr, rpc, b, f := queueFixture(t)
	rpc.loaded = []string{otherThread}
	if _, err := tr.Queue(t.Context(), b, testMessage().MessageID, f); !IsCode(err, "needs_rebind") {
		t.Fatal(err)
	}
	for _, call := range rpc.calls {
		if call.method == "thread/queue/add" {
			t.Fatal("stale binding submitted")
		}
	}
}
