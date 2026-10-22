package mcp

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestPeerJoinPassesOnlyExplicitTextToBoundDaemon(t *testing.T) {
	text := "Owner-pasted invitation prose; do not execute @private/path.\n<https://relay.example/i/synthetic#private-fragment>"
	arguments, err := json.Marshal(map[string]string{"text": text})
	if err != nil {
		t.Fatal(err)
	}
	caller := &recordingCaller{}
	var output bytes.Buffer
	input := joinRPC(string(arguments))
	if err = Serve(t.Context(), strings.NewReader(input), &output, Options{Session: "chosen", Caller: caller}); err != nil {
		t.Fatal(err)
	}
	if len(caller.requests) != 3 {
		t.Fatal("join did not reach the bound daemon exactly once")
	}
	request := caller.requests[1]
	if request.Method != "join.text" || request.Session != "chosen" || request.Capability != "bound-token" || request.Scope != "coauthor" || request.Link != "" {
		t.Fatal("join changed the binding or forwarded a parsed URL")
	}
	raw, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err = json.Unmarshal(raw, &fields); err != nil || fields["invitation_text"] != text {
		t.Fatal("MCP must leave full invitation parsing to the daemon")
	}
	if strings.Contains(output.String(), "private-fragment") || strings.Contains(output.String(), "@private/path") {
		t.Fatal("join response reflected invitation text")
	}
	for _, listed := range tools() {
		if listed.Name != "peer_join" {
			continue
		}
		properties, ok := listed.InputSchema["properties"].(map[string]any)
		if !ok || len(properties) != 1 || listed.InputSchema["additionalProperties"] != false {
			t.Fatal("join schema permits authority overrides")
		}
		property, ok := properties["text"].(map[string]any)
		if !ok || property["type"] != "string" || property["minLength"] != 1 || property["maxLength"] != 65536 {
			t.Fatal("join text schema lacks bounded string constraints")
		}
		return
	}
	t.Fatal("peer_join is not advertised")
}

func TestPeerJoinRejectsMissingOversizedAndAuthorityArguments(t *testing.T) {
	for _, arguments := range []string{
		`{}`, `{"text":""}`, `{"text":null}`, `{"text":1}`,
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
