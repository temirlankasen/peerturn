package daemon

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestRuntimePointerPublishedForDaemonLifetimeAndRestart(t *testing.T) {
	admin := routingKey(t)
	f := newRelayFixture(t, admin)
	d := newDaemonFixture(t, f, "runtime-pointer", admin)
	pointer := filepath.Join(d.dir, "runtime.path")
	check := func() {
		t.Helper()
		root, err := os.OpenRoot(d.dir)
		if err != nil {
			t.Fatal(err)
		}
		raw, readErr := root.ReadFile("runtime.path")
		err = errors.Join(readErr, root.Close())
		if err != nil || string(raw) != d.runtime+"\n" {
			t.Fatalf("active daemon did not publish runtime: %q %v", raw, err)
		}
		info, err := os.Lstat(pointer)
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || !ownedJournal(info) {
			t.Fatalf("runtime pointer is not a private owned file: %v", err)
		}
	}
	check()
	d.stop(t)
	if _, err := os.Lstat(pointer); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("clean stop retained runtime pointer: %v", err)
	}
	// A crash may retain a safe pointer after the socket has gone away.
	if err := os.WriteFile(pointer, []byte(d.runtime+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	d.start(t)
	check()
	d.stop(t)
	if _, err := os.Lstat(pointer); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("restart stop retained runtime pointer: %v", err)
	}
}
