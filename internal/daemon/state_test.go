package daemon

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/temirlankasen/peerturn/internal/fault"
	"github.com/temirlankasen/peerturn/internal/localipc"
)

func stateDir(t *testing.T) string {
	t.Helper()
	t.Setenv("TMPDIR", "/tmp")
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(dir, "state")
}

func TestInitializeRequiresExplicitStorageAndSession(t *testing.T) {
	for _, o := range []InitOptions{
		{StateDir: stateDir(t), RelayURL: "https://relay.example", SessionLabel: "test"},
		{StateDir: stateDir(t), RelayURL: "https://relay.example", FilePermissions: true},
		{StateDir: stateDir(t), RelayURL: "http://relay.example", SessionLabel: "test", FilePermissions: true},
	} {
		if _, err := Initialize(t.Context(), o); err == nil {
			t.Fatal("unsafe initialization accepted")
		}
	}
}

func TestInitializeSealsIdentityAndDoesNotReplace(t *testing.T) {
	dir := stateDir(t)
	o := InitOptions{StateDir: dir, RelayURL: "https://relay.example", SessionLabel: "private-label-canary", FilePermissions: true}
	status, err := Initialize(t.Context(), o)
	if err != nil {
		t.Fatal(err)
	}
	if status.Binding != "test_session" || status.Capabilities.AttachExisting {
		t.Fatalf("unsupported binding advertised: %+v", status)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := root.ReadFile("client.sealed")
	if closeErr := root.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte(o.SessionLabel)) {
		t.Fatal("plaintext label in journal")
	}
	if _, err = Initialize(t.Context(), o); !fault.IsCode(err, fault.Conflict) {
		t.Fatalf("reinit: %v", err)
	}
	d, err := Open(t.Context(), Options{StateDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if closeErr := d.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	}()
	if _, err = d.Dispatch(context.Background(), localipc.Request{Method: "send", Session: "other", Peer: "unknown", Kind: "coauthor", Body: "hi"}); err == nil {
		t.Fatal("foreign session accepted")
	}
	if _, err = d.Dispatch(t.Context(), localipc.Request{Method: "send", Session: o.SessionLabel, Peer: "unknown", Kind: "ask", Body: "hi"}); err == nil {
		t.Fatal("ask admitted")
	}
}
