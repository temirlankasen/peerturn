package store

import (
	"slices"
	"strings"
	"testing"

	"github.com/temirlankasen/peerturn/internal/fault"
)

func TestInboxPagesKeepTiedTimestampsWithoutDuplicatesOrOmissions(t *testing.T) {
	s := testStore(t)
	f := fixture(t, s)
	for seq := uint64(1); seq <= 4; seq++ {
		commitOne(t, s, f, seq)
	}
	want := []string{"00000000000000000000000000000002", "00000000000000000000000000000003", "00000000000000000000000000000004", f.message.MessageID}
	var got []string
	after := ""
	for range 4 {
		items, err := s.ListInboxAfter(t.Context(), after, 2)
		if err != nil {
			t.Fatal(err)
		}
		if len(items) == 0 {
			break
		}
		for _, item := range items {
			got = append(got, item.MessageID)
			if string(item.Body) != f.message.BodyUTF8 || item.GrantID != "grant_one" {
				t.Fatal("page lost the existing body or grant binding")
			}
		}
		after = items[len(items)-1].MessageID
	}
	if !slices.Equal(got, want) {
		t.Fatalf("unstable tied-timestamp pages: got %v want %v", got, want)
	}
	first, err := s.ListInbox(t.Context(), 2)
	if err != nil || len(first) != 2 || first[0].MessageID != want[0] || first[1].MessageID != want[1] {
		t.Fatal("legacy list did not delegate to the first page", err)
	}
	// A later timestamp must sort after the cursor even when its ID is smaller.
	if _, err = s.db.ExecContext(t.Context(), `UPDATE inbox SET created_at=? WHERE id=?`, testNow+1, want[0]); err != nil {
		t.Fatal(err)
	}
	later, err := s.ListInboxAfter(t.Context(), f.message.MessageID, 2)
	if err != nil || len(later) != 1 || later[0].MessageID != want[0] {
		t.Fatal("cursor compared only IDs instead of the timestamp and ID tuple", err)
	}
}

func TestInboxPagesPreserveFiltersAndRejectInvalidCursors(t *testing.T) {
	s := testStore(t)
	f := fixture(t, s)
	for seq := uint64(1); seq <= 4; seq++ {
		commitOne(t, s, f, seq)
	}
	const control = "00000000000000000000000000000002"
	const parked = "00000000000000000000000000000003"
	if _, err := s.db.ExecContext(t.Context(), `UPDATE inbox SET lane='control' WHERE id=?`, control); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(t.Context(), `UPDATE inbox SET state='parked' WHERE id=?`, parked); err != nil {
		t.Fatal(err)
	}
	items, err := s.ListInboxAfter(t.Context(), control, 10)
	if err != nil || len(items) != 2 || items[0].MessageID != "00000000000000000000000000000004" || items[1].MessageID != f.message.MessageID {
		t.Fatal("pagination exposed a control or parked message", err)
	}
	for _, cursor := range []string{"bad", strings.Repeat("A", 32), "../state.db", "' OR 1=1"} {
		if _, err = s.ListInboxAfter(t.Context(), cursor, 10); !fault.IsCode(err, fault.InvalidEncoding) {
			t.Fatal("invalid inbox cursor accepted", err)
		}
	}
	if _, err = s.ListInboxAfter(t.Context(), "", 0); !fault.IsCode(err, fault.InvalidEncoding) {
		t.Fatal("invalid page limit accepted", err)
	}
	items, err = s.ListInboxAfter(t.Context(), strings.Repeat("1", 32), 10)
	if err != nil || len(items) != 0 {
		t.Fatal("missing cursor silently restarted pagination", err)
	}
	if _, err = s.db.ExecContext(t.Context(), `UPDATE inbox SET expires_at=? WHERE id=?`, testNow, control); err != nil {
		t.Fatal(err)
	}
	items, err = s.ListInboxAfter(t.Context(), control, 10)
	if err != nil || len(items) != 0 {
		t.Fatal("expired cursor silently restarted pagination", err)
	}
}
