package codex

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestQueueContractPinnedOwnerShape(t *testing.T) {
	fixture, err := os.ReadFile("testdata/codex-0.163.0-owner-queue-list.json")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		client any
		omit   bool
		valid  bool
	}{
		{name: "owner_uuid", valid: true},
		{name: "null", client: nil},
		{name: "missing", omit: true},
		{name: "number", client: 7},
		{name: "object", client: map[string]any{}},
		{name: "array", client: []any{}},
		{name: "boolean", client: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var response struct {
				Data       []map[string]any `json:"data"`
				NextCursor any              `json:"nextCursor"`
			}
			if err := json.Unmarshal(fixture, &response); err != nil {
				t.Fatal(err)
			}
			if !tc.valid {
				response.Data[0]["clientUserMessageId"] = tc.client
			}
			if tc.omit {
				delete(response.Data[0], "clientUserMessageId")
			}
			reply, err := json.Marshal(response)
			if err != nil {
				t.Fatal(err)
			}
			rpc := &queueContractRPC{ownerTurnRPC: &ownerTurnRPC{fakeRPC: newFakeRPC(), thread: map[string]any{
				"id": testThread, "status": map[string]string{"type": "active"}}}, queueReply: string(reply)}
			tr := NewTransport(rpc, testVersion)
			binding, caps, err := tr.Probe(t.Context(), testThread)
			if !caps.AttachExisting || binding.ThreadID != testThread {
				t.Fatalf("queue shape invalidated live binding: %+v %v", caps, err)
			}
			if tc.valid {
				if err != nil || !caps.WakeIdle || len(caps.SupportedScopes) != 1 || caps.SupportedScopes[0] != "coauthor" {
					t.Fatalf("owner queue item closed coauthor: %+v %v", caps, err)
				}
			} else if !IsCode(err, "runtime_contract_changed") || caps.WakeIdle || len(caps.SupportedScopes) != 0 {
				t.Fatalf("malformed queue item retained coauthor: %+v %v", caps, err)
			}
			frame, err := Frame(testMessage(), strings.Repeat("d", 64))
			if err != nil {
				t.Fatal(err)
			}
			_, err = tr.Queue(t.Context(), binding, testMessage().MessageID, frame)
			if tc.valid && err != nil || !tc.valid && !IsCode(err, "runtime_contract_changed") {
				t.Fatalf("queue admission: %v", err)
			}
			adds := 0
			for _, call := range rpc.calls {
				if call.method == "thread/queue/add" {
					adds++
				}
			}
			if tc.valid && adds != 1 || !tc.valid && adds != 0 {
				t.Fatalf("native submit count = %d", adds)
			}
		})
	}
}
