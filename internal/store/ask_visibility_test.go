package store

import (
	"testing"

	"github.com/temirlankasen/peerturn/internal/fault"
	"github.com/temirlankasen/peerturn/internal/protocol"
)

func TestAskCustodyNeitherSpendsTurnsNorExposesLiveInbox(t *testing.T) {
	s := testStore(t)
	f := fixture(t, s)
	f.message.Kind = protocol.Ask
	commitOne(t, s, f, 1)
	var turns int
	if err := s.db.QueryRowContext(t.Context(), `SELECT turns FROM sessions WHERE id=?`, bindingKey(f.recipient.cert.Binding)).Scan(&turns); err != nil || turns != 0 {
		t.Errorf("ask custody spent a model turn before fork reservation: %d %v", turns, err)
	}
	items, err := s.ListInbox(t.Context(), 100)
	if err != nil || len(items) != 0 {
		t.Errorf("ask body exposed to live inbox: %d %v", len(items), err)
	}
	if _, err = s.InboxEntry(t.Context(), f.message.MessageID); !fault.IsCode(err, NotFound) {
		t.Errorf("live reply API resolves restricted ask: %v", err)
	}
}
