package store

import (
	"bytes"
	"context"
	"database/sql"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/temirlankasen/peerturn/internal/fault"
	"github.com/temirlankasen/peerturn/internal/protocol"
)

func TestMigrationDurabilityAndNewerRefusal(t *testing.T) {
	dir := testPrivateDir(t)
	sealer, err := CreateFileSealer(filepath.Join(dir, "key"))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "state.db")
	s, err := Open(t.Context(), path, Options{Sealer: sealer})
	if err != nil {
		t.Fatal(err)
	}
	f := fixture(t, s)
	request := InboxRequest{Packet: f.packet(t, 1), Mailbox: "mailbox_b", MailboxSeq: 1, GrantID: "grant_one", Open: f.options, Receipt: f.receipt}
	if _, err = s.InboxCommit(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(t.Context(), path, Options{Sealer: sealer})
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.InboxCommit(t.Context(), request)
	if err != nil || !got.Duplicate || got.Frontier != 1 {
		t.Fatalf("lost custody: %+v %v", got, err)
	}
	for pragma, want := range map[string]int{"foreign_keys": 1, "synchronous": 2, "busy_timeout": 10000, "fullfsync": 1, "checkpoint_fullfsync": 1} {
		var value int
		query := map[string]string{"foreign_keys": "PRAGMA foreign_keys", "synchronous": "PRAGMA synchronous", "busy_timeout": "PRAGMA busy_timeout", "fullfsync": "PRAGMA fullfsync", "checkpoint_fullfsync": "PRAGMA checkpoint_fullfsync"}[pragma]
		if err = s.db.QueryRowContext(t.Context(), query).Scan(&value); err != nil || value != want {
			t.Fatalf("%s = %d: %v", pragma, value, err)
		}
	}
	var journal string
	if err = s.db.QueryRowContext(t.Context(), "PRAGMA journal_mode").Scan(&journal); err != nil || journal != "wal" {
		t.Fatalf("WAL %s %v", journal, err)
	}
	if _, err = s.db.ExecContext(t.Context(), `INSERT INTO schema_version VALUES(2)`); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err = Open(t.Context(), path, Options{Sealer: sealer}); !fault.IsCode(err, NewerSchema) {
		t.Fatalf("newer accepted: %v", err)
	}
}

func TestNoPlaintextAtRestWithPositiveControl(t *testing.T) {
	s := testStore(t)
	f := fixture(t, s)
	commitOne(t, s, f, 1)
	const marker = "unique-store-plaintext-canary"
	if err := s.CreateInvite(t.Context(), "AAAAAAAAAAAAAAAAAAAAAA", testNow+100); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ConsumeInvite(t.Context(), "AAAAAAAAAAAAAAAAAAAAAA", "AAAAAAAAAAAAAAAAAAAAAQ", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", []byte(marker+"-invite"), testNow); err != nil {
		t.Fatal(err)
	}
	const attempt = "11111111111111111111111111111111"
	if _, err := s.DeliveryIntent(t.Context(), f.message.MessageID, attempt, testNow); err != nil {
		t.Fatal(err)
	}
	evidence := protocol.ReceiptPayload{MessageID: f.message.MessageID, AttemptID: attempt, Stage: "adapter_accepted", EvidenceKind: "native_queue", At: testNow, NativeInputID: marker + "-native-id"}
	if err := s.AuthorizeDelivery(t.Context(), evidence.MessageID, attempt, testNow); err != nil {
		t.Fatal(err)
	}
	if err := s.DeliveryResult(t.Context(), attempt, evidence, []byte(marker+"-native-result")); err != nil {
		t.Fatal(err)
	}
	var path string
	var seq int
	var name string
	if err := s.db.QueryRowContext(t.Context(), `PRAGMA database_list`).Scan(&seq, &name, &path); err != nil {
		t.Fatal(err)
	}
	scan := func(path string) bool {
		t.Helper()
		file, err := openLocalFile(path, os.O_RDONLY)
		if err != nil {
			t.Fatal(err)
		}
		raw, err := io.ReadAll(file)
		if closeErr := file.Close(); closeErr != nil {
			t.Fatal(closeErr)
		}
		if err != nil {
			t.Fatal(err)
		}
		return bytes.Contains(raw, []byte(marker))
	}
	for _, suffix := range []string{"", "-wal", "-shm"} {
		if scan(path + suffix) {
			t.Fatalf("plaintext in %s", suffix)
		}
	}
	positiveDir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	positive := filepath.Join(positiveDir, "positive.db")
	db, err := sql.Open("sqlite", positive)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(t.Context(), `CREATE TABLE control(body TEXT); INSERT INTO control VALUES(?)`, marker); err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	if !scan(positive) {
		t.Fatal("plaintext detector positive control failed")
	}
	if _, err = s.db.ExecContext(t.Context(), `PRAGMA wal_checkpoint(FULL)`); err != nil {
		t.Fatal(err)
	}
	if scan(path) {
		t.Fatal("checkpoint leaked plaintext")
	}
}

func TestTransactionContextFailureDoesNotCommit(t *testing.T) {
	s := testStore(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := s.CreateInvite(ctx, "AAAAAAAAAAAAAAAAAAAAAA", 100); !fault.IsCode(err, StorageUnavailable) {
		t.Fatalf("cancelled write cause: %v", err)
	}
	if _, err := s.ConsumeInvite(t.Context(), "AAAAAAAAAAAAAAAAAAAAAA", "AAAAAAAAAAAAAAAAAAAAAQ", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", []byte("result"), 10); !fault.IsCode(err, NotFound) {
		t.Fatalf("partial cancelled write: %v", err)
	}
}

func TestFailedInitialMigrationRollsBackSchemaLedger(t *testing.T) {
	dir := testPrivateDir(t)
	sealer, err := CreateFileSealer(filepath.Join(dir, "key"))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "state.db")
	file, err := openLocalFile(path, os.O_CREATE|os.O_RDWR)
	if err != nil {
		t.Fatal(err)
	}
	if err = file.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if closeErr := db.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	})
	if _, err = db.ExecContext(t.Context(), `CREATE TABLE inbox(blocked INTEGER)`); err != nil {
		t.Fatal(err)
	}
	if _, err = Open(t.Context(), path, Options{Sealer: sealer}); !fault.IsCode(err, StorageUnavailable) {
		t.Fatalf("incompatible migration accepted: %v", err)
	}
	var count int
	if err = db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM sqlite_master WHERE name IN ('schema_version','grants','identities')`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("partial schema/ledger: %d %v", count, err)
	}
}
