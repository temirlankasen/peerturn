package daemon

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/temirlankasen/peerturn/internal/localipc"
)

func TestDarwinMCPRuntime(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", "")
	// Keep synthetic Unix socket names below the platform sun_path limit.
	t.Setenv("TMPDIR", "/tmp")
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", base)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(base, "config"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(base, "state"))
	dir, err := DefaultRuntimeDir()
	if err != nil || dir != filepath.Join(base, "peerturn-"+strconv.Itoa(os.Geteuid())) {
		t.Fatalf("CLI/MCP macOS runtime: %s %v", dir, err)
	}
	if err = os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	// Deliberately broaden this fixture's permissions to exercise refusal.
	if err = os.Chmod(dir, info.Mode().Perm()|0o055); err != nil {
		t.Fatal(err)
	}
	if server, listenErr := localipc.Listen(dir); listenErr == nil {
		if closeErr := server.Close(); closeErr != nil {
			t.Fatal(closeErr)
		}
		t.Fatal("missing XDG allowed a shared runtime")
	}
	if err = os.Chmod(dir, info.Mode().Perm()); err != nil {
		t.Fatal(err)
	}
	server, err := localipc.Listen(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err = server.Close(); err != nil {
		t.Fatal(err)
	}
}
