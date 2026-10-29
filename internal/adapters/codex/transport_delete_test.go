package codex

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

type deleteRPC struct {
	*fakeRPC
	deleted bool
	err     error
	reply   string
}

func (r *deleteRPC) Call(ctx context.Context, method string, params, result any) error {
	if method != "thread/queue/delete" {
		return r.fakeRPC.Call(ctx, method, params, result)
	}
	encoded, err := json.Marshal(params)
	if err != nil {
		return err
	}
	var p map[string]any
	if err = json.Unmarshal(encoded, &p); err != nil {
		return err
	}
	r.calls = append(r.calls, rpcCall{method: method, params: p})
	if r.err != nil {
		return r.err
	}
	if r.reply != "" {
		return json.Unmarshal([]byte(r.reply), result)
	}
	encoded, err = json.Marshal(map[string]bool{"deleted": r.deleted})
	if err != nil {
		return err
	}
	return json.Unmarshal(encoded, result)
}

func transportDeleter(t *testing.T, tr *Transport) interface {
	Delete(context.Context, Binding, string) (bool, error)
} {
	t.Helper()
	deleter, ok := any(tr).(interface {
		Delete(context.Context, Binding, string) (bool, error)
	})
	if !ok {
		t.Fatal("native transport cannot delete revoked queue entries")
	}
	return deleter
}

func TestDeleteQueueEntryIsIdempotentAndWorksWhenUnloaded(t *testing.T) {
	rpc := &deleteRPC{fakeRPC: newFakeRPC(), deleted: true}
	tr := NewTransport(rpc, testVersion)
	b, err := tr.Bind(t.Context(), testThread)
	if err != nil {
		t.Fatal(err)
	}
	deleter := transportDeleter(t, tr)
	rpc.loaded = nil
	for _, wantDeleted := range []bool{true, false} {
		rpc.deleted = wantDeleted
		deleted, deleteErr := deleter.Delete(t.Context(), b, "queue-item-1")
		if deleteErr != nil || deleted != wantDeleted {
			t.Fatalf("deleted=%v, error=%v", deleted, deleteErr)
		}
		last := rpc.calls[len(rpc.calls)-1]
		if last.method != "thread/queue/delete" || len(last.params) != 2 || last.params["threadId"] != testThread || last.params["queuedSubmissionId"] != "queue-item-1" {
			t.Fatalf("wrong delete target: %+v", last)
		}
	}
}

func TestDeleteRejectsForeignIdentityAndInvalidItemBeforeRPC(t *testing.T) {
	rpc := &deleteRPC{fakeRPC: newFakeRPC()}
	tr := NewTransport(rpc, testVersion)
	b, err := tr.Bind(t.Context(), testThread)
	if err != nil {
		t.Fatal(err)
	}
	deleter := transportDeleter(t, tr)
	before := len(rpc.calls)
	for _, alter := range []func(*Binding){
		func(b *Binding) { b.ThreadID = "wrong" },
		func(b *Binding) { b.DaemonID += "wrong" },
		func(b *Binding) { b.Version += "wrong" },
	} {
		changed := b
		alter(&changed)
		if deleted, deleteErr := deleter.Delete(t.Context(), changed, "queue-item-1"); deleted || !IsCode(deleteErr, "needs_rebind") {
			t.Fatalf("foreign identity: deleted=%v, error=%v", deleted, deleteErr)
		}
	}
	if deleted, deleteErr := deleter.Delete(t.Context(), b, "bad\nitem"); deleted || !IsCode(deleteErr, "invalid_input") {
		t.Fatalf("invalid item: deleted=%v, error=%v", deleted, deleteErr)
	}
	if len(rpc.calls) != before {
		t.Fatal("invalid delete reached RPC")
	}
}

func TestDeleteFailureNeverClaimsDequeue(t *testing.T) {
	for _, tc := range []struct {
		err   error
		reply string
	}{
		{err: errors.New("lost deletion ACK")},
		{reply: `{}`},
	} {
		rpc := &deleteRPC{fakeRPC: newFakeRPC(), err: tc.err, reply: tc.reply}
		tr := NewTransport(rpc, testVersion)
		b, err := tr.Bind(t.Context(), testThread)
		if err != nil {
			t.Fatal(err)
		}
		if deleted, deleteErr := transportDeleter(t, tr).Delete(t.Context(), b, "queue-item-1"); deleted || !IsCode(deleteErr, "in_doubt") {
			t.Fatalf("deleted=%v, error=%v", deleted, deleteErr)
		}
	}
}
