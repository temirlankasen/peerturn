package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/temirlankasen/peerturn/internal/localipc"
)

func TestSetupRejectsHostAliasWithWritableParent(t *testing.T) {
	for _, host := range []string{"claude", "codex"} {
		for _, sticky := range []bool{false, true} {
			t.Run(host+map[bool]string{false: "/writable", true: "/sticky"}[sticky], func(t *testing.T) {
				root, pin, prefix := setupObservedFixture(t, host)
				parent := t.TempDir()
				alias := filepath.Join(parent, "untrusted-alias")
				if err := os.Symlink(root, alias); err != nil {
					t.Fatal(err)
				}
				mode := os.FileMode(0777)
				if sticky {
					mode |= os.ModeSticky
				}
				if err := os.Chmod(parent, mode); err != nil {
					t.Fatal(err)
				}
				marketplaces, _ := observedSetupJSON(t, host, alias, filepath.Join(root, prefix))
				t.Setenv("SETUP_MARKETPLACES", marketplaces)
				calls := filepath.Join(t.TempDir(), "calls")
				t.Setenv("SETUP_CALLS", calls)
				var stdout, stderr bytes.Buffer
				code := runSetup([]string{host, "--path", root, "--sha256", pin}, &stdout, &stderr)
				var result struct{ Error *localipc.Error }
				if code == 0 || json.Unmarshal(stdout.Bytes(), &result) != nil || result.Error == nil || result.Error.Code != "marketplace_conflict" {
					t.Fatalf("unsafe host alias accepted: code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
				}
				if got := string(readSetupHostCalls(t, calls)); got != "--version\nplugin marketplace list --json\n" {
					t.Fatalf("unsafe alias caused host mutation: %s", got)
				}
			})
		}
	}
}
