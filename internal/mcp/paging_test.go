package mcp

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestInboxContinuationIsOnlyAnOpaqueLocalCursor(t *testing.T) {
	cursor := strings.Repeat("a", 32)
	request, err := toolRequest("peer_inbox", json.RawMessage(`{"after":"`+cursor+`"}`))
	if err != nil || request.Method != "inbox" || request.Cursor != cursor {
		t.Fatalf("cursor not forwarded: %v", err)
	}
	for _, args := range []string{`{"after":"x","url":"https://relay.example"}`, `{"session":"native"}`, `{"key":"secret"}`} {
		if _, err = toolRequest("peer_inbox", json.RawMessage(args)); err == nil {
			t.Fatal("arbitrary authority argument accepted")
		}
	}
}
