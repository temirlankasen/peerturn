package store

import (
	"testing"

	"github.com/temirlankasen/peerturn/internal/fault"
)

func TestStoreRejectsSealedRowAndColumnSubstitution(t *testing.T) {
	for _, column := range []string{"row", "column"} {
		t.Run(column, func(t *testing.T) {
			s := testStore(t)
			f := fixture(t, s)
			commitOne(t, s, f, 1)
			commitOne(t, s, f, 2)
			source, target := f.message.MessageID, "00000000000000000000000000000002"
			statement := `UPDATE inbox SET body=(SELECT body FROM inbox WHERE id=?) WHERE id=?`
			if column == "column" {
				statement = `UPDATE inbox SET body=(SELECT packet FROM inbox WHERE id=?) WHERE id=?`
				target = source
			}
			if _, err := s.db.ExecContext(t.Context(), statement, source, target); err != nil {
				t.Fatal(err)
			}
			if _, err := s.Inbox(t.Context(), target); !fault.IsCode(err, SealingFailed) {
				t.Fatalf("substituted blob accepted: %v", err)
			}
		})
	}
}
