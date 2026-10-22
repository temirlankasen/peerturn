package daemon

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/temirlankasen/peerturn/internal/localipc"
)

func TestStateDirectoryHasExclusiveLifetimeAcrossRuntimeDirectories(t *testing.T) {
	o := InitOptions{StateDir: stateDir(t), RelayURL: "https://relay.example", SessionLabel: "lock-test", FilePermissions: true}
	if _, err := Initialize(t.Context(), o); err != nil {
		t.Fatal(err)
	}
	d, err := Open(t.Context(), Options{StateDir: o.StateDir, RuntimeDir: filepath.Join(t.TempDir(), "first")})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if closeErr := d.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	}()
	other, err := Open(t.Context(), Options{StateDir: o.StateDir, RuntimeDir: filepath.Join(t.TempDir(), "second")})
	if err == nil {
		if closeErr := other.Close(); closeErr != nil {
			t.Error(closeErr)
		}
		t.Fatal("two journals can mutate the same state directory")
	}
	if localipc.PublicError(err).Code != "already_running" {
		t.Fatalf("state lifetime lock = %v", err)
	}
	if _, err = Initialize(t.Context(), o); localipc.PublicError(err).Code != "already_running" {
		t.Fatalf("init bypassed active journal ownership: %v", err)
	}
	if err = d.Close(); err != nil {
		t.Fatal(err)
	}
	replacement, err := Open(t.Context(), Options{StateDir: o.StateDir})
	if err != nil {
		t.Fatalf("Close did not release journal ownership: %v", err)
	}
	if err := replacement.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestInitializeLocksStateBeforeCreatingIdentity(t *testing.T) {
	dir := stateDir(t)
	lock, err := localipc.LockStateDir(dir, true)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := lock.Close(); err != nil {
			t.Error(err)
		}
	}()
	o := InitOptions{StateDir: dir, RelayURL: "https://relay.example", SessionLabel: "init-lock", FilePermissions: true}
	if _, err := Initialize(t.Context(), o); localipc.PublicError(err).Code != "already_running" {
		t.Fatalf("initializer bypassed existing state lock: %v", err)
	}
	for _, name := range []string{"seal.key", "client.sealed", "state.db", "config.toml"} {
		if _, err := os.Lstat(filepath.Join(dir, name)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("rejected initializer touched %s: %v", name, err)
		}
	}
	if err := lock.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := Initialize(t.Context(), o); err != nil {
		t.Fatalf("initializer did not acquire released ownership: %v", err)
	}
}

func TestOpenRejectsSharedJournalAndReleasesFailedOwnership(t *testing.T) {
	o := InitOptions{StateDir: stateDir(t), RelayURL: "https://relay.example", SessionLabel: "journal-owner", FilePermissions: true}
	if _, err := Initialize(t.Context(), o); err != nil {
		t.Fatal(err)
	}
	journal := filepath.Join(o.StateDir, "client.sealed")
	link := filepath.Join(o.StateDir, "shared-journal")
	if err := os.Link(journal, link); err != nil {
		t.Fatal(err)
	}
	if d, err := Open(t.Context(), Options{StateDir: o.StateDir}); err == nil {
		if err := d.Close(); err != nil {
			t.Error(err)
		}
		t.Fatal("shared journal inode accepted")
	}
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	d, err := Open(t.Context(), Options{StateDir: o.StateDir})
	if err != nil {
		t.Fatalf("failed Open retained the state lock: %v", err)
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestPlatformDefaultsKeepFinalSymlinkUnresolved(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dir, "target")
	if err = os.Mkdir(target, 0700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "peerturn")
	if err = os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	got, err := canonicalDefault(link)
	if err != nil || got != link {
		t.Fatalf("final default component resolved to another private directory: %s %v", got, err)
	}
	if server, err := localipc.Listen(got); err == nil {
		if err := server.Close(); err != nil {
			t.Error(err)
		}
		t.Fatal("symlink default became a live runtime directory")
	}
}

func TestRuntimeDefaultFailsClosedWithoutPrivateEnvironment(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", "")
	t.Setenv("TMPDIR", "")
	if _, err := DefaultRuntimeDir(); localipc.PublicError(err).Code != "runtime_unavailable" {
		t.Fatalf("missing private temp directory fell back to shared /tmp: %v", err)
	}
}
