package store

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/temirlankasen/peerturn/internal/fault"
)

func TestDurableEncryptedOpen(t *testing.T) {
	dir := testPrivateDir(t)
	key := filepath.Join(dir, "key")
	sealer, err := CreateFileSealer(key)
	if err != nil {
		t.Fatal(err)
	}
	plain := []byte("positive-control-custody-secret")
	sealed, err := sealer.Seal([]byte("inbox/id"), plain)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(sealed, plain) {
		t.Fatal("plaintext seal")
	}
	reopened, err := OpenFileSealer(key)
	if err != nil {
		t.Fatal(err)
	}
	got, err := reopened.Open([]byte("inbox/id"), sealed)
	if err != nil || !bytes.Equal(got, plain) {
		t.Fatalf("reopen: %v", err)
	}
	if _, err = reopened.Open([]byte("outbox/id"), sealed); !fault.IsCode(err, SealingFailed) {
		t.Fatalf("cross-record substitution: %v", err)
	}
	info, err := os.Stat(key)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("key mode: %v", err)
	}
	s, err := Open(context.Background(), filepath.Join(dir, "state.db"), Options{Sealer: sealer})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
}

func testStore(t *testing.T) *Store {
	t.Helper()
	dir := testPrivateDir(t)
	sealer, err := CreateFileSealer(filepath.Join(dir, "key"))
	if err != nil {
		t.Fatal(err)
	}
	s, err := Open(t.Context(), filepath.Join(dir, "state.db"), Options{Sealer: sealer, Clock: testClock})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	return s
}

func testPrivateDir(t *testing.T) string {
	t.Helper()
	path, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(path, "private")
}

func testClock() time.Time { return time.Unix(testNow, 0) }
