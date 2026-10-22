package store

import (
	"bytes"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"testing"

	"github.com/temirlankasen/peerturn/internal/fault"
)

func TestKeyCreationNeverReplacesExistingKey(t *testing.T) {
	path := filepath.Join(testPrivateDir(t), "key")
	sealer, err := CreateFileSealer(path)
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := sealer.Seal([]byte("test-key"), []byte("retained-value"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = CreateFileSealer(path); !fault.IsCode(err, fault.Conflict) {
		t.Fatalf("key replacement: %v", err)
	}
	opened, err := OpenFileSealer(path)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := opened.Open([]byte("test-key"), sealed)
	if err != nil || !bytes.Equal(plain, []byte("retained-value")) {
		t.Fatalf("original key lost: %v", err)
	}
}

func TestConcurrentKeyCreationPublishesOneCompleteKey(t *testing.T) {
	path := filepath.Join(testPrivateDir(t), "key")
	var group sync.WaitGroup
	results := make(chan error, 24)
	for range 24 {
		group.Go(func() {
			_, err := CreateFileSealer(path)
			if fault.IsCode(err, fault.Conflict) {
				if _, openErr := OpenFileSealer(path); openErr != nil {
					results <- openErr
					return
				}
			}
			results <- err
		})
	}
	group.Wait()
	close(results)
	winners := 0
	for err := range results {
		if err == nil {
			winners++
		} else if !fault.IsCode(err, fault.Conflict) {
			t.Fatalf("partial/concurrent key: %v", err)
		}
	}
	if winners != 1 {
		t.Fatalf("creation winners = %d", winners)
	}
	if _, err := OpenFileSealer(path); err != nil {
		t.Fatalf("published key is incomplete: %v", err)
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil || len(entries) != 1 || entries[0].Name() != "key" {
		t.Fatalf("temporary key remains: %v, %v", entries, err)
	}
}

func TestRestrictiveUmaskStillCreatesPrivateUsableState(t *testing.T) {
	dir := testPrivateDir(t)
	old := syscall.Umask(0777)
	defer syscall.Umask(old)
	sealer, err := CreateFileSealer(filepath.Join(dir, "key"))
	if err != nil {
		t.Fatal(err)
	}
	s, err := Open(t.Context(), filepath.Join(dir, "state.db"), Options{Sealer: sealer})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]os.FileMode{dir: 0700, filepath.Join(dir, "key"): 0600, filepath.Join(dir, "state.db"): 0600} {
		info, statErr := os.Lstat(path)
		if statErr != nil || info.Mode().Perm() != want {
			t.Fatalf("umask state mode: %v", statErr)
		}
	}
}

func TestCreateRelativeKeyPathRefused(t *testing.T) {
	if _, err := CreateFileSealer("keys/store.key"); !fault.IsCode(err, fault.InvalidEncoding) {
		t.Fatalf("relative key creation: %v", err)
	}
}
