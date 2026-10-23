package daemon

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/temirlankasen/peerturn/internal/localipc"
)

func runtimePointerFixture(t *testing.T) (string, string, string) {
	t.Helper()
	t.Setenv("TMPDIR", "/tmp")
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(base, "config"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(base, "state"))
	t.Setenv("XDG_RUNTIME_DIR", "")
	state, err := DefaultStateDir()
	if err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(state, 0700); err != nil {
		t.Fatal(err)
	}
	runtimeDir := filepath.Join(base, "r")
	if err = os.Mkdir(runtimeDir, 0700); err != nil {
		t.Fatal(err)
	}
	pointer := filepath.Join(state, "runtime.path")
	if err = os.WriteFile(pointer, []byte(runtimeDir+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	return state, runtimeDir, pointer
}

func TestRuntimePointerBridgesMissingRuntimeEnvironment(t *testing.T) {
	_, runtimeDir, _ := runtimePointerFixture(t)
	t.Setenv("TMPDIR", "")
	got, err := DefaultRuntimeDir()
	if err != nil || got != runtimeDir {
		t.Fatalf("private runtime pointer not selected: %q %v", got, err)
	}
}

func TestRuntimePointerExplicitEnvironmentWins(t *testing.T) {
	_, _, pointer := runtimePointerFixture(t)
	sharedMode := os.FileMode(0644)
	if err := os.Chmod(pointer, sharedMode); err != nil {
		t.Fatal(err)
	}
	base := filepath.Dir(pointer)
	t.Setenv("XDG_RUNTIME_DIR", base)
	want := filepath.Join(base, "peerturn", strconv.Itoa(os.Geteuid()))
	got, err := DefaultRuntimeDir()
	if err != nil || got != want {
		t.Fatalf("explicit runtime did not take precedence: %q %v", got, err)
	}
}

func TestRuntimePointerRejectsHostilePointerWithoutFallback(t *testing.T) {
	for _, change := range []string{"symlink", "loose_permissions", "hardlink", "oversize", "relative", "noncanonical", "directory", "missing_runtime", "runtime_symlink", "runtime_permissions", "state_permissions", "state_symlink"} {
		t.Run(change, func(t *testing.T) {
			state, runtimeDir, pointer := runtimePointerFixture(t)
			sharedDirectoryMode := os.FileMode(0755)
			var err error
			switch change {
			case "symlink":
				err = os.Rename(pointer, pointer+".target")
				if err == nil {
					err = os.Symlink(pointer+".target", pointer)
				}
			case "loose_permissions":
				sharedMode := os.FileMode(0644)
				err = os.Chmod(pointer, sharedMode)
			case "hardlink":
				err = os.Link(pointer, pointer+".link")
			case "oversize":
				err = os.WriteFile(pointer, []byte(strings.Repeat("x", 4097)), 0600)
			case "relative":
				err = os.WriteFile(pointer, []byte("relative/runtime\n"), 0600)
			case "noncanonical":
				err = os.WriteFile(pointer, []byte(runtimeDir+"/../r\n"), 0600)
			case "directory":
				err = os.Remove(pointer)
				if err == nil {
					err = os.Mkdir(pointer, 0700)
				}
			case "missing_runtime":
				err = os.Remove(runtimeDir)
			case "runtime_symlink":
				err = os.Rename(runtimeDir, runtimeDir+".target")
				if err == nil {
					err = os.Symlink(runtimeDir+".target", runtimeDir)
				}
			case "runtime_permissions":
				err = os.Chmod(runtimeDir, sharedDirectoryMode)
			case "state_permissions":
				err = os.Chmod(state, sharedDirectoryMode)
			case "state_symlink":
				err = os.Rename(state, state+".target")
				if err == nil {
					err = os.Symlink(state+".target", state)
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			if got, err := DefaultRuntimeDir(); err == nil || got != "" {
				t.Fatalf("unsafe pointer fell back to another runtime: %q %v", got, err)
			}
		})
	}
}

func TestRuntimePointerEndpointStillUsesPrivateSocketChecks(t *testing.T) {
	_, runtimeDir, _ := runtimePointerFixture(t)
	server, err := localipc.Listen(runtimeDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if closeErr := server.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	})
	sharedSocketMode := os.FileMode(0666)
	if err = os.Chmod(filepath.Join(runtimeDir, localipc.SocketName), sharedSocketMode); err != nil {
		t.Fatal(err)
	}
	got, err := DefaultRuntimeDir()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = (localipc.Client{RuntimeDir: got}).Call(t.Context(), localipc.Request{Method: "status"}); err == nil {
		t.Fatal("pointer bypassed private socket checks")
	}
}

func TestRuntimePointerAbsentPreservesOfflineFailure(t *testing.T) {
	_, _, pointer := runtimePointerFixture(t)
	if err := os.Remove(pointer); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", "")
	if _, err := DefaultRuntimeDir(); localipc.PublicError(err).Code != "runtime_unavailable" {
		t.Fatalf("absent pointer changed offline failure: %v", err)
	}
	if _, err := os.Lstat(pointer); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("runtime lookup created pointer: %v", err)
	}
}
