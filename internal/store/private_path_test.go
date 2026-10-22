package store

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/temirlankasen/peerturn/internal/fault"
)

func TestUnsafeFilePermissionsFailWithExactCode(t *testing.T) {
	for _, target := range []string{"key", "db", "dir"} {
		t.Run(target, func(t *testing.T) {
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
			if err = s.Close(); err != nil {
				t.Fatal(err)
			}
			selected, mode := key, os.FileMode(0644)
			switch target {
			case "db":
				selected = path
			case "dir":
				selected, mode = dir, 0755
			}
			if err = os.Chmod(selected, mode); err != nil {
				t.Fatal(err)
			}
			if target == "key" {
				if _, err = OpenFileSealer(key); !fault.IsCode(err, SealingFailed) {
					t.Fatalf("public key file accepted: %v", err)
				}
			} else if _, err = Open(t.Context(), path, Options{Sealer: sealer}); !fault.IsCode(err, StorageUnavailable) {
				t.Fatalf("public state accepted: %v", err)
			}
			if target == "dir" {
				if _, err = OpenFileSealer(key); !fault.IsCode(err, StorageUnavailable) {
					t.Fatalf("public key directory accepted: %v", err)
				}
			}
		})
	}
}

func TestSymlinkKeyDatabaseAndParentsRefused(t *testing.T) {
	for _, target := range []string{"key", "db", "dir", "ancestor"} {
		t.Run(target, func(t *testing.T) {
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
			if err = s.Close(); err != nil {
				t.Fatal(err)
			}
			source := key
			switch target {
			case "db":
				source = path
			case "dir":
				source = dir
			case "ancestor":
				source = filepath.Dir(dir)
			}
			if err = os.Rename(source, source+"-actual"); err != nil {
				t.Fatal(err)
			}
			if err = os.Symlink(filepath.Base(source)+"-actual", source); err != nil {
				t.Fatal(err)
			}
			if target == "key" {
				if _, err = OpenFileSealer(key); !fault.IsCode(err, SealingFailed) {
					t.Fatalf("symlink key accepted: %v", err)
				}
			} else if _, err = Open(t.Context(), path, Options{Sealer: sealer}); !fault.IsCode(err, StorageUnavailable) {
				t.Fatalf("symlink state accepted: %v", err)
			}
			if target == "dir" || target == "ancestor" {
				if _, err = OpenFileSealer(key); !fault.IsCode(err, StorageUnavailable) {
					t.Fatalf("symlink key parent accepted: %v", err)
				}
			}
		})
	}
}

type ownerInfo struct{ uid uint32 }

func (i ownerInfo) Name() string       { return "owned" }
func (i ownerInfo) Size() int64        { return 32 }
func (i ownerInfo) Mode() os.FileMode  { return 0600 }
func (i ownerInfo) ModTime() time.Time { return time.Time{} }
func (i ownerInfo) IsDir() bool        { return false }
func (i ownerInfo) Sys() any           { return &syscall.Stat_t{Uid: i.uid} }

func TestPrivateFileRejectsWrongOwner(t *testing.T) {
	info, err := os.Stat(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		t.Fatal("owner metadata unavailable")
	}
	current := stat.Uid
	if !privateFile(ownerInfo{current}) || privateFile(ownerInfo{current ^ 1}) {
		t.Fatal("file ownership was ignored")
	}
}

func TestWritableAncestorCannotRedirectState(t *testing.T) {
	dir := testPrivateDir(t)
	ancestor, err := os.OpenRoot(filepath.Dir(dir))
	if err != nil {
		t.Fatal(err)
	}
	// Deliberately make the fixture unsafe to verify the refusal boundary.
	chmodErr := ancestor.Chmod(".", 0777)
	closeErr := ancestor.Close()
	if chmodErr != nil || closeErr != nil {
		t.Fatalf("unsafe fixture: %v %v", chmodErr, closeErr)
	}
	if _, err = CreateFileSealer(filepath.Join(dir, "key")); !fault.IsCode(err, StorageUnavailable) {
		t.Fatalf("unsafe writable ancestor accepted: %v", err)
	}
}

func TestUncleanPathsCannotChangeSQLitePathResolution(t *testing.T) {
	dir := testPrivateDir(t)
	sealer, err := CreateFileSealer(filepath.Join(dir, "key"))
	if err != nil {
		t.Fatal(err)
	}
	ambiguous := dir + "/child/../"
	if _, err = CreateFileSealer(ambiguous + "other-key"); !fault.IsCode(err, fault.InvalidEncoding) {
		t.Fatalf("unclean key creation accepted: %v", err)
	}
	if _, err = OpenFileSealer(ambiguous + "key"); !fault.IsCode(err, fault.InvalidEncoding) {
		t.Fatalf("unclean key open accepted: %v", err)
	}
	if _, err = Open(t.Context(), ambiguous+"state.db", Options{Sealer: sealer}); !fault.IsCode(err, fault.InvalidEncoding) {
		t.Fatalf("unclean SQLite path accepted: %v", err)
	}
}
