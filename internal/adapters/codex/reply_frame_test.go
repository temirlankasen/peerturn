package codex

import (
	"strings"
	"testing"

	"github.com/temirlankasen/peerturn/internal/protocol"
)

func replyMessage() protocol.MessagePayload {
	m := testMessage()
	m.Kind = protocol.Reply
	m.ParentID = strings.Repeat("e", 32)
	m.ReplyTo = m.ParentID
	m.Hop = 1
	return m
}

// A reply under a coauthor grant wakes the thread like a new coauthor message,
// so its frame must reach the live queue and stay recognisable as peer data.
func TestCoauthorReplyEntersLiveQueue(t *testing.T) {
	m := replyMessage()
	f, err := Frame(m, strings.Repeat("d", 64))
	if err != nil {
		t.Fatalf("reply frame: %v", err)
	}
	if id, ok := PeerMessageID(f); !ok || id != m.MessageID {
		t.Fatalf("reply frame not recognised as peer data: %q %v", id, ok)
	}
	rpc := newFakeRPC()
	tr := NewTransport(rpc, testVersion)
	b, err := tr.Bind(t.Context(), testThread)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tr.Queue(t.Context(), b, m.MessageID, f); IsCode(err, "scope_denied") || IsCode(err, "invalid_input") {
		t.Fatalf("reply refused by the live queue: %v", err)
	}
}

func TestLiveFrameStillRefusesInform(t *testing.T) {
	m := replyMessage()
	m.Kind = protocol.Inform
	if _, err := Frame(m, strings.Repeat("d", 64)); !IsCode(err, "scope_denied") {
		t.Fatalf("inform entered the live frame: %v", err)
	}
}
