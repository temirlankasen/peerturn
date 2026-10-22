package mcp

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/temirlankasen/peerturn/internal/localipc"
)

func TestMCPJoinEscapingAndOversizeDoNotTerminateProxy(t *testing.T) {
	for _, tc := range []struct {
		name, text string
		accepted   bool
	}{
		{"escaped-byte-limit", strings.Repeat("\x01", localipc.MaxInvitationTextBytes), true},
		{"escaped-over-byte-limit", strings.Repeat("\x01", localipc.MaxInvitationTextBytes+1), false},
		{"multibyte-over-byte-limit", strings.Repeat("界", localipc.MaxInvitationTextBytes/3+1), false},
		{"over-frame-limit", strings.Repeat("x", 1024*1024), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			arguments, err := json.Marshal(map[string]string{"text": tc.text})
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
				if join.Error != nil || len(caller.requests) != 3 || caller.requests[1].Method != "join.text" || caller.requests[1].InvitationText != tc.text {
					t.Fatal("escaped text within the byte limit did not reach daemon")
				}
			} else if join.Error == nil || join.Error.Code != -32602 || len(caller.requests) != 2 {
				t.Fatal("oversized input did not return a typed argument rejection")
			}
		})
	}
}
