package daemon

import (
	"os"
	"path/filepath"
	"testing"
)

var daemonTestSlots = make(chan struct{}, 2)

func parallelDaemonTest(t *testing.T) {
	t.Helper()
	t.Parallel()
	// FULL durability shares disk bandwidth even across independent databases.
	// Bound simultaneous fixtures to keep the durable handshake's disk queue short.
	select {
	case daemonTestSlots <- struct{}{}:
		t.Cleanup(func() { <-daemonTestSlots })
	case <-t.Context().Done():
		t.Fatal("test canceled while waiting for a daemon fixture slot")
	}
}

func TestStateDirectoryFixturePreservesEnvironment(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	want := os.Getenv("TMPDIR")
	dir := stateDir(t)
	if got := os.Getenv("TMPDIR"); got != want {
		t.Fatalf("state fixture changed process TMPDIR: got %q, want %q", got, want)
	}
	if resolved, err := filepath.EvalSymlinks(filepath.Dir(dir)); err != nil || resolved != filepath.Dir(dir) {
		t.Fatalf("fixture parent is not canonical: %q, %v", resolved, err)
	}
}

func TestStateDirectoryFixtureSupportsParallelIsolation(t *testing.T) {
	t.Parallel()
	for range 2 {
		t.Run("private", func(t *testing.T) {
			t.Parallel()
			dir := stateDir(t)
			parent := filepath.Dir(dir)
			info, err := os.Stat(parent)
			if err != nil || info.Mode().Perm() != 0700 {
				t.Fatalf("fixture parent is not private: %v, %v", info, err)
			}
			// Independent state locks and initialization must coexist.
			if _, err = Initialize(t.Context(), InitOptions{StateDir: dir, RelayURL: "https://relay.example", SessionLabel: "parallel-fixture", FilePermissions: true}); err != nil {
				t.Fatal(err)
			}
		})
	}
}
