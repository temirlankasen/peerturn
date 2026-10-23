package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSetupVerifiesBeforeHostCommands(t *testing.T) {
	root := t.TempDir()
	payload := []byte("verified plugin")
	if err := os.WriteFile(filepath.Join(root, "plugin.json"), payload, 0600); err != nil {
		t.Fatal(err)
	}
	manifest := fmt.Appendf(nil, "%x  plugin.json\n", sha256.Sum256(payload))
	if err := os.WriteFile(filepath.Join(root, ".peerturn-files.sha256"), manifest, 0600); err != nil {
		t.Fatal(err)
	}
	pin := fmt.Sprintf("%x", sha256.Sum256(manifest))
	if err := verifySetupTree(root, pin); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "plugin.json"), []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := verifySetupTree(root, pin); err == nil {
		t.Fatal("modified package accepted")
	}
	if err := os.WriteFile(filepath.Join(root, "plugin.json"), payload, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "extra.js"), payload, 0600); err != nil {
		t.Fatal(err)
	}
	if err := verifySetupTree(root, pin); err == nil {
		t.Fatal("unlisted executable content accepted")
	}
}

func TestSetupRejectsMissingFilesTraversalAndSymlinks(t *testing.T) {
	for _, path := range []string{"missing.js", "../outside.js", "link.js"} {
		t.Run(path, func(t *testing.T) {
			root := t.TempDir()
			if path == "link.js" {
				if err := os.Symlink("outside.js", filepath.Join(root, path)); err != nil {
					t.Fatal(err)
				}
			}
			manifest := fmt.Appendf(nil, "%x  %s\n", sha256.Sum256(nil), path)
			if err := os.WriteFile(filepath.Join(root, ".peerturn-files.sha256"), manifest, 0600); err != nil {
				t.Fatal(err)
			}
			if err := verifySetupTree(root, fmt.Sprintf("%x", sha256.Sum256(manifest))); err == nil {
				t.Fatal("unsafe or missing content accepted")
			}
		})
	}
}

func TestSetupHostPlansAndActivationLimits(t *testing.T) {
	for _, host := range []string{"claude", "codex"} {
		root, _, prefix := setupObservedFixture(t, host)
		marketplaces, plugins := observedSetupJSON(t, host, root, filepath.Join(root, prefix))
		var calls [][]string
		invoke := func(_ context.Context, name string, args []string, output io.Writer) error {
			calls = append(calls, append([]string{name}, args...))
			switch strings.Join(args, " ") {
			case "--version":
				_, err := io.WriteString(output, qualifiedSetupVersion(host))
				return err
			case "plugin marketplace list --json":
				_, err := io.WriteString(output, marketplaces)
				return err
			case "plugin list --json":
				_, err := io.WriteString(output, plugins)
				return err
			case "mcp list --json":
				_, err := io.WriteString(output, "[]")
				return err
			case "mcp get peerturn", "mcp get peerturn --json":
				return absentSetupMCP(host, output)
			}
			return nil
		}
		var output bytes.Buffer
		if err := installSetup(context.Background(), host, root, &output, invoke); err != nil {
			t.Fatal(err)
		}
		count := 8
		if host == "claude" {
			count = 9
		}
		if len(calls) != count || calls[0][1] != "--version" || calls[2][3] != "add" || !sameSetupPath(calls[2][4], root) || calls[4][3] != "peerturn@peerturn" {
			t.Fatalf("unexpected host plan: %v", calls)
		}
		if host == "claude" && (len(calls[4]) != 6 || calls[4][5] != "peerturn_binary="+filepath.Join(root, "peerturn")) {
			t.Fatalf("verified helper was not configured as one argument: %v", calls)
		}
		if !strings.Contains(output.String(), "hook trust") || !strings.Contains(output.String(), "activation") {
			t.Fatal("setup pretended installation proved activation")
		}
	}
}

func TestSetupRejectsUnverifiedPath(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := runInput([]string{"setup", "codex", "--path", t.TempDir()}, strings.NewReader(""), &stdout, &stderr); code == 0 {
		t.Fatal("unverified tree accepted")
	}
}
