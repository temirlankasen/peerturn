package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/temirlankasen/peerturn/internal/localipc"
)

func TestSetupClaudeMCPNativePolicyPreserved(t *testing.T) {
	for _, change := range []string{"ours", "command", "args", "env", "type", "timeout", "unknown", "null", "duplicate", "malformed", "oversize", "project", "project_identical", "ancestor", "local", "managed", "legacy", "symlink", "legacy_symlink", "query_failed"} {
		t.Run(change, func(t *testing.T) {
			config := setupMCPHome(t)
			root := t.TempDir()
			entry := map[string]any{"type": "stdio", "command": filepath.Join(root, "peerturn"), "args": []string{"mcp", "--current"}, "env": map[string]string{}}
			switch change {
			case "command":
				entry[change] = "/foreign"
			case "args":
				entry[change] = []string{"mcp", "--session", "foreign"}
			case "env":
				entry[change] = map[string]string{"OWNER_SETTING": "private-marker"}
			case "type":
				entry[change] = "http"
			case "timeout":
				entry[change] = 5000
			case "unknown":
				entry["tools"] = []string{"peer_status"}
			}
			settings := map[string]any{"mcpServers": map[string]any{"peerturn": entry}, "privateUnrelatedSetting": "private-marker"}
			cwd, err := os.Getwd()
			if err != nil {
				t.Fatal(err)
			}
			switch change {
			case "project", "project_identical", "ancestor":
				project := cwd
				if change == "ancestor" {
					child := filepath.Join(cwd, "child")
					if mkdirErr := os.Mkdir(child, 0700); mkdirErr != nil {
						t.Fatal(mkdirErr)
					}
					t.Chdir(child)
				}
				peer := any(entry)
				if change != "project_identical" {
					peer = map[string]string{"type": "http", "url": "https://example.invalid/mcp"}
				}
				writeSetupMCPJSON(t, filepath.Join(project, ".mcp.json"), map[string]any{"mcpServers": map[string]any{"peerturn": peer}})
			case "local":
				settings["projects"] = map[string]any{cwd: map[string]any{"mcpServers": map[string]any{"peerturn": entry}}}
			case "managed":
				writeSetupMCPJSON(t, filepath.Join(config, "managed-settings.json"), map[string]any{})
			}
			data, err := json.Marshal(settings)
			if err != nil {
				t.Fatal(err)
			}
			switch change {
			case "null":
				data = []byte(`{"mcpServers":null}`)
			case "duplicate":
				data = append([]byte(`{"mcpServers":{},`), data[1:]...)
			case "malformed":
				data = []byte("{")
			case "oversize":
				data = []byte(strings.Repeat(" ", setupClaudeConfigLimit+1))
			}
			path := filepath.Join(config, ".claude.json")
			if change == "legacy" {
				path = filepath.Join(config, ".config.json")
			}
			if writeErr := os.WriteFile(path, data, 0600); writeErr != nil {
				t.Fatal(writeErr)
			}
			switch change {
			case "symlink", "legacy_symlink":
				link := filepath.Join(config, ".config.json")
				target := path
				if change == "symlink" {
					target = filepath.Join(config, "target.json")
					if renameErr := os.Rename(path, target); renameErr != nil {
						t.Fatal(renameErr)
					}
					link = path
				}
				if symlinkErr := os.Symlink(target, link); symlinkErr != nil {
					t.Fatal(symlinkErr)
				}
			}
			var calls []string
			invoke := func(_ context.Context, _ string, args []string, _ io.Writer) error {
				calls = append(calls, strings.Join(args, " "))
				if change == "query_failed" {
					return setupHostExit(1)
				}
				return nil
			}
			err = registerSetupMCP(t.Context(), "claude", root, invoke)
			if change == "ours" || change == "legacy" {
				if err != nil || len(calls) != 1 || calls[0] != "mcp get peerturn" {
					t.Fatalf("identical user entry not skipped: calls=%v err=%v", calls, err)
				}
			} else if problem, typed := errors.AsType[*localipc.Error](err); !typed || problem.Code != "mcp_registration_failed" || strings.Contains(problem.Message, "private-marker") {
				t.Fatalf("conflict not safely reported: %v", err)
			}
			for _, call := range calls {
				if strings.HasPrefix(call, "mcp add ") {
					t.Fatalf("existing/uncertain entry caused a write: %v", calls)
				}
			}
			directory, openErr := os.OpenRoot(config)
			if openErr != nil {
				t.Fatal(openErr)
			}
			preserved := filepath.Base(path)
			if change == "symlink" {
				preserved = "target.json"
			}
			unchanged, readErr := directory.ReadFile(preserved)
			closeErr := directory.Close()
			if readErr != nil || closeErr != nil || string(unchanged) != string(data) {
				t.Fatal("owner native configuration changed")
			}
		})
	}
}

func writeSetupMCPJSON(t *testing.T, path string, value any) {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestSetupCodexMCPUncertainListingNeverAdded(t *testing.T) {
	for _, data := range []string{"", "null", "{}", "[null]", `[{}]`, `[{"name":1}]`, `[{"name":"peerturn","name":"other"}]`, `[{"name":"peerturn"},{"name":"peerturn"}]`, strings.Repeat(" ", 65537)} {
		t.Run(data[:min(len(data), 40)], func(t *testing.T) {
			var calls []string
			invoke := func(_ context.Context, _ string, args []string, output io.Writer) error {
				calls = append(calls, strings.Join(args, " "))
				_, err := io.WriteString(output, data)
				return err
			}
			if err := registerSetupMCP(t.Context(), "codex", t.TempDir(), invoke); err == nil || len(calls) != 1 || calls[0] != "mcp list --json" {
				t.Fatalf("uncertain listing caused a write: calls=%v err=%v", calls, err)
			}
		})
	}
}
