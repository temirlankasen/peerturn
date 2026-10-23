package mcp

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestMCPJoinEscapingAndOversizeDoNotTerminateProxy(t *testing.T) {
	for _, tc := range []struct {
		name, text string
		accepted   bool
	}{
		{"escaped-byte-limit", strings.Repeat("a", 12), true},
		{"escaped-over-byte-limit", strings.Repeat("\x01", 12+1), false},
		{"multibyte-over-byte-limit", strings.Repeat("界", 12/3+1), false},
		{"over-frame-limit", strings.Repeat("x", 1024*1024), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			arguments, err := json.Marshal(map[string]string{"handle": tc.text})
			if err != nil {
				t.Fatal(err)
			}
			caller := &recordingCaller{}
			var output bytes.Buffer
			input := joinRPC(string(arguments)) + `{"jsonrpc":"2.0","id":3,"method":"ping"}` + "\n"
			if err = Serve(t.Context(), strings.NewReader(input), &output, Options{Session: "chosen", Caller: caller}); err != nil {
				t.Fatalf("invalid input terminated MCP proxy: %v", err)
			}
			lines := strings.Split(strings.TrimSpace(output.String()), "\n")
			if len(lines) != 3 {
				t.Fatalf("got %d responses; later ping was lost", len(lines))
			}
			var join, ping rpcResponse
			if err = json.Unmarshal([]byte(lines[1]), &join); err != nil {
				t.Fatal(err)
			}
			if err = json.Unmarshal([]byte(lines[2]), &ping); err != nil {
				t.Fatal(err)
			}
			if ping.Error != nil || string(ping.ID) != "3" {
				t.Fatal("proxy did not process later ping")
			}
			if tc.accepted {
				if join.Error != nil || len(caller.requests) != 3 || caller.requests[1].Method != "join.owner" || caller.requests[1].InvitationHandle != tc.text {
					t.Fatal("escaped text within the byte limit did not reach daemon")
				}
			} else if join.Error == nil || join.Error.Code != -32602 || len(caller.requests) != 2 {
				t.Fatal("oversized input did not return a typed argument rejection")
			}
		})
	}
}
