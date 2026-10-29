package mcp

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestMCPJoinInstructionsUseOwnerHook(t *testing.T) {
	input := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"fixture","version":"1"}}}` + "\n"
	var output bytes.Buffer
	if err := Serve(t.Context(), strings.NewReader(input), &output, Options{Session: "chosen", Caller: &recordingCaller{}}); err != nil {
		t.Fatal(err)
	}
	var response struct {
		Result struct {
			Instructions string `json:"instructions"`
		} `json:"result"`
	}
	if err := json.Unmarshal(output.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	for _, instruction := range []string{"peer_join({})", "owner-prompt hook", "handle"} {
		if !strings.Contains(response.Result.Instructions, instruction) {
			t.Errorf("missing join instruction %q", instruction)
		}
	}
	if strings.Contains(response.Result.Instructions, "accepts explicit owner-pasted invitation text") {
		t.Error("join instructions still request an unsupported secret argument")
	}
}
