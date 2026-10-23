package mcp

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestPeerJoinWithoutModelSecret(t *testing.T) {
	for _, arguments := range []string{`{}`, `{"handle":"0123456789ab"}`} {
		caller := &recordingCaller{}
		var output bytes.Buffer
		if err := Serve(t.Context(), strings.NewReader(joinRPC(arguments)), &output, Options{Session: "chosen", Caller: caller}); err != nil {
			t.Fatal(err)
		}
		if len(caller.requests) != 3 {
			t.Fatal("pending join did not reach bound daemon")
		}
		r := caller.requests[1]
		if r.Method != "join.owner" || r.Session != "chosen" || r.Capability != "bound-token" || r.Scope != "coauthor" || r.Link != "" || r.InvitationText != "" {
			t.Fatal("MCP did not use a secret-free bound request")
		}
	}
	for _, tool := range tools() {
		if tool.Name == "peer_join" {
			raw, err := json.Marshal(tool.InputSchema)
			if err != nil || bytes.Contains(raw, []byte(`"text"`)) || !bytes.Contains(raw, []byte(`"handle"`)) {
				t.Fatal("join schema exposes secret text")
			}
			return
		}
	}
	t.Fatal("peer_join missing")
}

func TestPeerJoinRejectsMissingOversizedAndAuthorityArguments(t *testing.T) {
	for _, arguments := range []string{
		`{"handle":""}`, `{"text":""}`, `{"text":null}`, `{"text":1}`,
		`{"text":"private-fragment","session":"other"}`,
		`{"text":"private-fragment","scope":"ask"}`,
		`{"text":"private-fragment","link":"https://evil.invalid"}`,
		`{"text":"private-fragment\u0000"}`,
		`{"text":"` + strings.Repeat("a", 65537) + `"}`,
		`{"text":"` + strings.Repeat("я", 32769) + `"}`,
	} {
		caller := &recordingCaller{}
		var output bytes.Buffer
		if err := Serve(t.Context(), strings.NewReader(joinRPC(arguments)), &output, Options{Session: "chosen", Caller: caller}); err != nil {
			t.Fatal(err)
		}
		if len(caller.requests) != 2 || caller.requests[1].Method != "bye" || !strings.Contains(output.String(), `"error"`) {
			t.Fatal("invalid join arguments escaped to daemon")
		}
		if strings.Contains(output.String(), "private-fragment") {
			t.Fatal("invalid join echoed private input")
		}
	}
}

func joinRPC(arguments string) string {
	return `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"fixture","version":"1"}}}` + "\n" +
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"peer_join","arguments":` + arguments + "}}\n"
}
