package mcp

import (
	"encoding/json"
	"testing"
)

func TestMCPAskIsExplicitSenderIntentOnly(t *testing.T) {
	r, err := toolRequest("peer_send", json.RawMessage(`{"peer":"peer","kind":"ask","body":"literal @path"}`))
	if err != nil || r.Kind != "ask" || r.Body != "literal @path" {
		t.Fatal("ask sender intent rejected", r, err)
	}
}
