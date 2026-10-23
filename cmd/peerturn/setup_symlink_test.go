package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSetupPinsExecutableAcrossUpdaterSymlinkChange(t *testing.T) {
	for _, host := range []string{"claude", "codex"} {
		t.Run(host, func(t *testing.T) {
			root, pin, _ := setupObservedFixture(t, host)
			binary, err := exec.LookPath(host)
			if err != nil {
				t.Fatal(err)
			}
			directory, err := os.OpenRoot(filepath.Dir(binary))
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if closeErr := directory.Close(); closeErr != nil {
					t.Error(closeErr)
				}
			}()
			program, err := directory.ReadFile(host)
			if err != nil {
				t.Fatal(err)
			}
			program = []byte(strings.Replace(string(program), "--version)", "--version) /bin/rm \"$SETUP_HOST_LINK\"; /bin/ln -s missing-updated-version \"$SETUP_HOST_LINK\";", 1))
			if err = directory.WriteFile(host, program, 0600); err != nil {
				t.Fatal(err)
			}
			if err = directory.Rename(host, "qualified-version"); err != nil {
				t.Fatal(err)
			}
			if err = directory.Symlink("qualified-version", host); err != nil {
				t.Fatal(err)
			}
			t.Setenv("SETUP_HOST_LINK", binary)
			t.Setenv("PATH", filepath.Dir(binary))
			var stdout, stderr bytes.Buffer
			if code := runSetup([]string{host, "--path", root, "--sha256", pin}, &stdout, &stderr); code != 0 {
				t.Fatalf("updater changed the executable after version probe: code=%d stderr=%s", code, stderr.String())
			}
		})
	}
}
