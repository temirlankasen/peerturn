package store

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/temirlankasen/peerturn/internal/fault"
)

func TestMissingKeyIsNeverCreatedByOpen(t *testing.T) {
	path := filepath.Join(testPrivateDir(t), "key")
	if _, err := OpenFileSealer(path); !fault.IsCode(err, SealingFailed) {
		t.Fatalf("missing key: %v", err)
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("open created missing key: %v", err)
	}
}

func TestRelativeKeyPathRefused(t *testing.T) {
	t.Chdir(t.TempDir())
	if _, err := OpenFileSealer("keys/store.key"); !fault.IsCode(err, fault.InvalidEncoding) {
		t.Fatalf("relative key: %v", err)
	}
	if _, err := os.Lstat("keys"); !os.IsNotExist(err) {
		t.Fatalf("relative path created directories: %v", err)
	}
}

func TestStoreChecksKeyBeforeAdmission(t *testing.T) {
	dir := testPrivateDir(t)
	key, path := filepath.Join(dir, "key"), filepath.Join(dir, "state.db")
	sealer, err := CreateFileSealer(key)
	if err != nil {
		t.Fatal(err)
	}
	s, err := Open(t.Context(), path, Options{Sealer: sealer})
	if err != nil {
		t.Fatal(err)
	}
	f := fixture(t, s)
	commitOne(t, s, f, 1)
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	wrong, err := CreateFileSealer(filepath.Join(testPrivateDir(t), "key"))
	if err != nil {
		t.Fatal(err)
	}
	if changed, openErr := Open(t.Context(), path, Options{Sealer: wrong}); !fault.IsCode(openErr, SealingFailed) {
		if changed != nil {
			if err = changed.Close(); err != nil {
				t.Fatal(err)
			}
		}
		t.Fatalf("mismatched key accepted: %v", openErr)
	}
	s, err = Open(t.Context(), path, Options{Sealer: sealer})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if closeErr := s.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	}()
	if _, err = s.Inbox(t.Context(), f.message.MessageID); err != nil {
		t.Fatalf("mismatch changed existing custody: %v", err)
	}
	if err = os.Remove(key); err != nil {
		t.Fatal(err)
	}
	if _, err = OpenFileSealer(key); !fault.IsCode(err, SealingFailed) {
		t.Fatalf("lost key regenerated: %v", err)
	}
	if _, err = os.Lstat(key); !os.IsNotExist(err) {
		t.Fatalf("lost key replaced: %v", err)
	}
}

func TestSQLiteTemporaryStorageStaysInMemory(t *testing.T) {
	s := testStore(t)
	var mode int
	if err := s.db.QueryRowContext(t.Context(), "PRAGMA temp_store").Scan(&mode); err != nil || mode != 2 {
		t.Fatalf("temp_store = %d: %v", mode, err)
	}
}

func TestStoreRefusesMissingOrCorruptedKeyCheck(t *testing.T) {
	for _, corruption := range []string{"missing", "corrupt"} {
		t.Run(corruption, func(t *testing.T) {
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
			query := `UPDATE key_check SET sealed=X'00' WHERE id=1`
			if corruption == "missing" {
				query = `DELETE FROM key_check WHERE id=1`
			}
			if _, err = s.db.ExecContext(t.Context(), query); err != nil {
				t.Fatal(err)
			}
			if err = s.Close(); err != nil {
				t.Fatal(err)
			}
			if opened, openErr := Open(t.Context(), path, Options{Sealer: sealer}); !fault.IsCode(openErr, SealingFailed) {
				if opened != nil {
					if err = opened.Close(); err != nil {
						t.Fatal(err)
					}
				}
				t.Fatalf("key check replaced: %v", openErr)
			}
		})
	}
}
