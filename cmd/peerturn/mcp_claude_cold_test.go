package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func claudeColdDiscovery() string {
	return `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"claude-code","version":"2.1.308"}}}` + "\n" +
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}` + "\n"
}

func claudeColdTool(id string) string {
	return `{"jsonrpc":"2.0","id":` + id + `,"method":"tools/call","params":{"name":"peer_status","arguments":{}}}` + "\n"
}

func TestMCPClaudeColdStartDiscoveryNeedsNoBinding(t *testing.T) {
	mcpRuntimeState(t)
	t.Setenv("CLAUDE_CODE_MESSAGING_SOCKET", "/synthetic/not-yet-created-native.sock")
	t.Setenv("CLAUDE_CODE_MESSAGING_TOKEN", "synthetic-private-native-token")
	var stdout, stderr bytes.Buffer
	code := runInput([]string{"mcp", "--current"}, strings.NewReader(claudeColdDiscovery()), &stdout, &stderr)
	if code != 0 || stderr.Len() != 0 || !strings.Contains(stdout.String(), `"peer_status"`) {
		t.Fatalf("cold discovery lost the MCP connection: code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if strings.Contains(stdout.String(), "synthetic-private-native-token") {
		t.Fatal("discovery disclosed native credentials")
	}
}

func TestMCPClaudeColdStartRefusesToolsButKeepsConnection(t *testing.T) {
	mcpRuntimeState(t)
	// Reach the native binding refusal without relying on macOS's TMPDIR fallback.
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	t.Setenv("CLAUDE_CODE_MESSAGING_SOCKET", "/synthetic/not-yet-created-native.sock")
	input := claudeColdDiscovery() + claudeColdTool("3") +
		`{"jsonrpc":"2.0","id":4,"method":"ping"}` + "\n"
	var stdout, stderr bytes.Buffer
	code := runInput([]string{"mcp", "--current"}, strings.NewReader(input), &stdout, &stderr)
	if code != 0 || stderr.Len() != 0 {
		t.Fatalf("unbound call terminated MCP: code=%d stderr=%s", code, stderr.String())
	}
	decoder := json.NewDecoder(&stdout)
	for i := range 4 {
		var response rpcCurrentResponse
		if err := decoder.Decode(&response); err != nil || response.Error != nil {
			t.Fatalf("cold-start protocol response %d: %v %+v", i, err, response)
		}
		if i == 2 && (!bytes.Contains(response.Result, []byte(`"isError":true`)) || !bytes.Contains(response.Result, []byte("needs_rebind"))) {
			t.Fatalf("unbound tool was not refused: %s", response.Result)
		}
		if i == 3 && !bytes.Equal(response.Result, []byte(`{}`)) {
			t.Fatalf("connection did not survive: %s", response.Result)
		}
	}
}
