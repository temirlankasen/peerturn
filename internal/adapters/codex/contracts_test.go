package codex

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/temirlankasen/peerturn/internal/runtimepolicy"
)

type contractShapeRPC struct {
	*fakeRPC
	method string
	reply  string
	err    error
}

func (r *contractShapeRPC) Call(ctx context.Context, method string, params, result any) error {
	if method != r.method {
		return r.fakeRPC.Call(ctx, method, params, result)
	}
	r.calls = append(r.calls, rpcCall{method: method})
	if r.err != nil {
		return r.err
	}
	return json.Unmarshal([]byte(r.reply), result)
}

func TestBindingContractFailuresCarryRecoveryAndScope(t *testing.T) {
	for _, tc := range []struct{ method, reply, id string }{
		{"thread/loaded/list", `{}`, "codex.loaded_list"},
		{"thread/loaded/list", `{"data":null}`, "codex.loaded_list"},
		{"thread/loaded/list", `{"data":[7]}`, "codex.loaded_list"},
		{"thread/read", `{}`, "codex.thread_read"},
		{"thread/read", `{"thread":{"id":"` + testThread + `"}}`, "codex.thread_read"},
	} {
		t.Run(tc.method+tc.reply, func(t *testing.T) {
			rpc := &contractShapeRPC{fakeRPC: newFakeRPC(), method: tc.method, reply: tc.reply}
			_, err := NewTransport(rpc, "0.164.0").Bind(t.Context(), testThread)
			failure, ok := errors.AsType[*runtimepolicy.Error](err)
			if !ok || failure.Code != "runtime_contract_changed" || failure.ContractID != tc.id || failure.DetectedVersion != "0.164.0" || failure.Action == "" {
				t.Fatalf("missing contract diagnostic: %+v", err)
			}
			if strings.Contains(failure.Action, testThread) || !strings.Contains(strings.Join(failure.AffectedCapabilities, ","), "binding") {
				t.Fatal("binding failure leaked identity or failed open")
			}
		})
	}
}

func TestTransientQueueProbePreservesBindingWithoutInventingContractFailure(t *testing.T) {
	rpc := &contractShapeRPC{fakeRPC: newFakeRPC(), method: "thread/queue/list", err: errors.New("temporary connection failure")}
	tr := NewTransport(rpc, "0.164.0")
	binding, err := tr.Bind(t.Context(), testThread)
	if err != nil {
		t.Fatal(err)
	}
	probed, caps, err := tr.Probe(t.Context(), testThread)
	if !IsCode(err, "unavailable") || IsCode(err, "runtime_contract_changed") || probed != binding || !caps.AttachExisting || caps.WakeIdle {
		t.Fatalf("transport loss misreported: binding=%+v caps=%+v error=%v", probed, caps, err)
	}
}

func TestMalformedSubmitAckRetainsAmbiguityAndContractFailure(t *testing.T) {
	for _, reply := range []string{`{}`, `{"queuedSubmission":7}`} {
		tr, rpc, binding, frame := queueFixture(t)
		rpc.queueReply = reply
		_, err := tr.Queue(t.Context(), binding, testMessage().MessageID, frame)
		failure, ok := errors.AsType[*runtimepolicy.Error](err)
		if !IsCode(err, "in_doubt") || !ok || failure.ContractID != "codex.queue_add" || failure.Action == "" {
			t.Fatalf("ambiguous response lost contract diagnostic: %v", err)
		}
		rpc.queueReply = ""
		if _, caps, probeErr := tr.Probe(t.Context(), testThread); !IsCode(probeErr, "runtime_contract_changed") || !caps.AttachExisting || caps.WakeIdle {
			t.Fatalf("read-only probe cleared a failed submit contract: %+v %v", caps, probeErr)
		}
		if _, nextErr := tr.Queue(t.Context(), binding, testMessage().MessageID, frame); !IsCode(nextErr, "runtime_contract_changed") || IsCode(nextErr, "in_doubt") {
			t.Fatalf("sticky failure became a fresh ambiguous effect: %v", nextErr)
		}
		adds := 0
		for _, call := range rpc.calls {
			if call.method == "thread/queue/add" {
				adds++
			}
		}
		if adds != 1 {
			t.Fatal("malformed submit was automatically resubmitted")
		}
	}
}
