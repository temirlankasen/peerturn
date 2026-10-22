package store

import (
	"bytes"
	"database/sql"
	"errors"
	"testing"
)

func TestRotateSealerReencryptsEveryRetainedField(t *testing.T) {
	s := testStore(t)
	f := fixture(t, s)
	commitOne(t, s, f, 1)
	seedRotationFields(t, s, f.message.MessageID)
	target, err := NewKeySealer(bytes.Repeat([]byte{2}, 32))
	if err != nil {
		t.Fatal(err)
	}
	old := s.sealer
	if err = s.RotateSealer(t.Context(), target); err != nil {
		t.Fatal(err)
	}
	if err = s.VerifySealer(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err = s.write(t.Context(), func(tx *sql.Tx) error {
		for _, column := range sealedColumns {
			records, readErr := readSealed(t.Context(), tx, column.query)
			if readErr != nil {
				return readErr
			}
			if len(records) == 0 {
				t.Fatalf("unexercised field %s", column.prefix)
			}
			for _, record := range records {
				if _, openErr := old.Open([]byte(column.prefix+record.id), record.sealed); openErr == nil {
					t.Fatalf("old key opens %s", column.prefix)
				}
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	var sealed []byte
	if err = s.db.QueryRowContext(t.Context(), `SELECT body FROM inbox LIMIT 1`).Scan(&sealed); err != nil {
		t.Fatal(err)
	}
	if _, err = old.Open([]byte("peerturn/store/v1/inbox/"+f.message.MessageID), sealed); err == nil {
		t.Fatal("old key still opens retained body")
	}
	if _, err = s.Inbox(t.Context(), f.message.MessageID); err != nil {
		t.Fatal(err)
	}
}

func seedRotationFields(t *testing.T, s *Store, messageID string) {
	t.Helper()
	seal := func(table, id string) []byte {
		data, err := s.seal(table, id, []byte("synthetic rotation field"))
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	statements := []struct {
		query string
		args  []any
	}{
		{`INSERT INTO invites VALUES('rotation_invite',?,0)`, []any{testNow + 60}},
		{`INSERT INTO invite_claims VALUES('rotation_invite','claim','digest',?,?)`, []any{seal("invite_claims", "rotation_invite"), testNow}},
		{`INSERT INTO outbox(id,grant_id,digest,packet,lane,stage,expires_at,bytes,created_at) VALUES('rotation_outbox','grant_one','digest',?,'data','sender_stored',?,1,?)`, []any{seal("outbox", "rotation_outbox"), testNow + 60, testNow}},
		{`INSERT INTO delivery_attempts VALUES('rotation_attempt',?,1,1,'intent',?,?)`, []any{messageID, seal("delivery_attempts", "rotation_attempt"), testNow}},
		{`INSERT INTO responder_runs(run_id,message_id,attempt_id,parent_binding,snapshot_handle,fork_native_id,model,effort,profile_hash,budget_id,state) SELECT 'rotation_run',?,'rotation_attempt',session_id,?,?,'model','high','profile','budget','completed' FROM bindings LIMIT 1`, []any{messageID, seal("responder_snapshot", "rotation_run"), seal("responder_fork", "rotation_run")}},
	}
	for _, statement := range statements {
		if _, err := s.db.ExecContext(t.Context(), statement.query, statement.args...); err != nil {
			t.Fatal(err)
		}
	}
}

type brokenRotationSealer struct{ Sealer }

func (s brokenRotationSealer) Seal(_, _ []byte) ([]byte, error) { return []byte("broken"), nil }

func TestRotateSealerRejectsInvalidReplacementWithoutPartialUpdates(t *testing.T) {
	s := testStore(t)
	f := fixture(t, s)
	commitOne(t, s, f, 1)
	old := s.sealer
	if err := s.RotateSealer(t.Context(), brokenRotationSealer{old}); err == nil {
		t.Fatal("invalid replacement accepted")
	}
	if err := s.VerifySealer(t.Context()); err != nil {
		t.Fatal(err)
	}
	if s.sealer != old {
		t.Fatal("failed migration changed active key")
	}
	s.abort = func(step string) error {
		if step == "before_commit" {
			return errors.New("crash")
		}
		return nil
	}
	target, err := NewKeySealer(bytes.Repeat([]byte{3}, 32))
	if err != nil {
		t.Fatal(err)
	}
	if err = s.RotateSealer(t.Context(), target); err == nil {
		t.Fatal("commit failure ignored")
	}
	s.abort = nil
	if err = s.VerifySealer(t.Context()); err != nil {
		t.Fatal(err)
	}
}
