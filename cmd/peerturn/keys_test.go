package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/temirlankasen/peerturn/internal/daemon"
	"github.com/temirlankasen/peerturn/internal/localipc"
)

func TestKeysIsolateRequiresOfflineLocalCommand(t *testing.T) {
	command, err := parseCommand([]string{"keys", "isolate", "--state-dir", "/tmp/peerturn-keys", "--json"})
	if err != nil || command.name != "keys isolate" || command.values["state-dir"] != "/tmp/peerturn-keys" {
		t.Fatal("missing offline rotation command", command.name, err)
	}
	if !localCommand("keys") {
		t.Fatal("key rotation not dispatched locally")
	}
	if _, err = parseCommand([]string{"keys", "isolate", "--peer", "untrusted"}); err == nil {
		t.Fatal("rotation accepts peer-controlled authority")
	}
}

func TestKeysIsolateRefusesDaemonStateLock(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err = root.Chmod(".", 0700); err != nil {
		t.Fatal(err)
	}
	if err = root.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err = daemon.Initialize(t.Context(), daemon.InitOptions{StateDir: dir, RelayURL: "https://relay.example", SessionLabel: "locked", FilePermissions: true}); err != nil {
		t.Fatal(err)
	}
	d, err := daemon.Open(t.Context(), daemon.Options{StateDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if closeErr := d.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	}()
	var stdout, stderr bytes.Buffer
	if code := runInput([]string{"keys", "isolate", "--state-dir", dir, "--json"}, bytes.NewReader(nil), &stdout, &stderr); code == 0 {
		t.Fatal("key isolation succeeded while daemon owns state")
	}
	var failure struct{ Error *localipc.Error }
	if err = json.Unmarshal(stdout.Bytes(), &failure); err != nil || failure.Error == nil {
		t.Fatal("missing isolation refusal", err)
	}
	want := "already_running"
	if runtime.GOOS == "darwin" {
		want = "macos_unqualified"
	}
	if failure.Error.Code != want {
		t.Fatalf("isolation refusal: got %s want %s", failure.Error.Code, want)
	}
	if _, err = os.Stat(filepath.Join(dir, "seal.key")); err != nil {
		t.Fatal("locked command removed file key", err)
	}
	if _, err = os.Stat(filepath.Join(dir, "wrapping.json")); !os.IsNotExist(err) {
		t.Fatal("locked command wrote migration metadata", err)
	}
}
