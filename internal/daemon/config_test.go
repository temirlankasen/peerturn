package daemon

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/temirlankasen/peerturn/internal/fault"
)

func configFixture(t *testing.T) (string, string) {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	state := filepath.Join(base, "state")
	if err = os.Mkdir(state, 0o700); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(base, "config", "config.toml"), state
}

func TestConfigAtomicPrivateAndIdempotent(t *testing.T) {
	path, state := configFixture(t)
	pin := publicKey(routingKey(t)[32:])
	var writers sync.WaitGroup
	for range 8 {
		writers.Go(func() {
			if err := WriteConfig(path, state, "https://relay.example", pin); err != nil {
				t.Error(err)
			}
		})
	}
	writers.Wait()
	config, err := ReadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if config.Version != 1 || config.StateDir != state || config.Relay != "https://relay.example" || config.StorageMode != "file_permissions" || config.PinnedRelayKey != pin {
		t.Fatalf("config = %+v", config)
	}
	for name, mode := range map[string]os.FileMode{path: 0o600, filepath.Dir(path): 0o700} {
		info, statErr := os.Stat(name)
		if statErr != nil || info.Mode().Perm() != mode {
			t.Fatalf("config path mode = %v %v", info, statErr)
		}
	}
	if err = WriteConfig(path, state, "https://other.example", pin); !fault.IsCode(err, fault.Conflict) {
		t.Fatalf("different config replaced existing choice: %v", err)
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil || len(entries) != 1 || entries[0].Name() != "config.toml" {
		t.Fatalf("temporary config files remain: %v %v", entries, err)
	}
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	raw, readErr := root.ReadFile("config.toml")
	err = errors.Join(readErr, root.Close())
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"session", "native", "private_key", "seed", "capability"} {
		if strings.Contains(string(raw), forbidden) {
			t.Fatalf("private field %s entered public config", forbidden)
		}
	}
}

func TestConfigRejectsUnknownMalformedAndOversized(t *testing.T) {
	for _, extra := range []string{"session='forbidden'\n", "version=2\n", strings.Repeat("#", 64*1024+1)} {
		path, state := configFixture(t)
		if err := WriteConfig(path, state, "https://relay.example", ""); err != nil {
			t.Fatal(err)
		}
		root, err := os.OpenRoot(filepath.Dir(path))
		if err != nil {
			t.Fatal(err)
		}
		file, err := root.OpenFile("config.toml", os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			t.Fatal(err)
		}
		_, writeErr := file.WriteString(extra)
		if err = errors.Join(writeErr, file.Close(), root.Close()); err != nil {
			t.Fatal(err)
		}
		if _, err = ReadConfig(path); err == nil {
			t.Fatal("invalid config accepted")
		}
	}
}

func TestConfigRejectsUnsafeFilesAndAncestors(t *testing.T) {
	for _, kind := range []string{"file_mode", "directory_mode", "file_symlink", "directory_symlink"} {
		t.Run(kind, func(t *testing.T) {
			path, state := configFixture(t)
			if err := WriteConfig(path, state, "https://relay.example", ""); err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "file_mode", "directory_mode":
				name := path
				if kind == "directory_mode" {
					name = filepath.Dir(path)
				}
				info, err := os.Stat(name)
				if err != nil {
					t.Fatal(err)
				}
				if err = os.Chmod(name, info.Mode().Perm()|0o044); err != nil {
					t.Fatal(err)
				}
			case "file_symlink":
				if err := os.Rename(path, path+".target"); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(path+".target", path); err != nil {
					t.Fatal(err)
				}
			case "directory_symlink":
				dir := filepath.Dir(path)
				if err := os.Rename(dir, dir+".target"); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(dir+".target", dir); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := ReadConfig(path); err == nil {
				t.Fatal("unsafe config path accepted")
			}
		})
	}
}

func TestConfiguredStateDirOnlyFallsBackForMissingConfig(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(base, "config"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(base, "default-state"))
	fallback, err := DefaultStateDir()
	if err != nil {
		t.Fatal(err)
	}
	if configured, configErr := ConfiguredStateDir(); configErr != nil || configured != fallback {
		t.Fatalf("missing config fallback = %q %v", configured, configErr)
	}
	path, err := DefaultConfigPath()
	if err != nil || path != filepath.Join(base, "config", "peerturn", "config.toml") {
		t.Fatalf("XDG config path = %q %v", path, err)
	}
	state := filepath.Join(base, "selected-state")
	if err = os.Mkdir(state, 0o700); err != nil {
		t.Fatal(err)
	}
	if err = WriteConfig(path, state, "https://relay.example", ""); err != nil {
		t.Fatal(err)
	}
	if configured, configErr := ConfiguredStateDir(); configErr != nil || configured != state {
		t.Fatalf("selected state = %q %v", configured, configErr)
	}
	if err = os.WriteFile(path, []byte("unexpected='field'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err = ConfiguredStateDir(); err == nil {
		t.Fatal("corrupt config silently fell back to another state")
	}
}

func TestConfigRejectsInvalidPublicSettings(t *testing.T) {
	path, state := configFixture(t)
	for _, test := range []struct{ state, relay, pin string }{
		{"relative-state", "https://relay.example", ""},
		{state, "http://relay.example", ""},
		{state, "https://relay.example/#fragment", ""},
		{state, "https://relay.example", "invalid-relay-key"},
	} {
		if err := WriteConfig(path, test.state, test.relay, test.pin); !fault.IsCode(err, fault.InvalidEncoding) {
			t.Fatalf("unsafe public setting = %v", err)
		}
	}
}
