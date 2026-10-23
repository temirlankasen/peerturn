package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/temirlankasen/peerturn/internal/localipc"
)

func TestSetupMCPUpgradeRequiresExplicitOwnerRemoval(t *testing.T) {
	for _, host := range []string{"codex", "claude"} {
		for _, change := range []string{"older", "preview_upgrade", "prerelease", "prerelease_numeric", "prerelease_newer", "same", "newer", "numeric_newer", "foreign_parent", "custom_layout", "relative", "unclean", "invalid_version", "args", "env", "policy", "query_failed"} {
			t.Run(host+"/"+change, func(t *testing.T) {
				config := setupMCPHome(t)
				packages := filepath.Join(filepath.Dir(config), ".local", "share", "peerturn")
				root := filepath.Join(packages, "0.2.0")
				command := filepath.Join(packages, "0.1.0-preview", "peerturn")
				want := "mcp_upgrade_required"
				switch change {
				case "preview_upgrade":
					root = filepath.Join(packages, "0.1.1-preview")
				case "prerelease":
					command = filepath.Join(packages, "0.2.0-preview.1", "peerturn")
				case "prerelease_numeric":
					root = filepath.Join(packages, "0.2.0-preview.10")
					command = filepath.Join(packages, "0.2.0-preview.2", "peerturn")
				case "prerelease_newer":
					root = filepath.Join(packages, "0.2.0-preview.2")
					command, want = filepath.Join(packages, "0.2.0-preview.10", "peerturn"), "mcp_registration_failed"
				case "same":
					command, want = filepath.Join(root, "peerturn"), ""
				case "newer", "numeric_newer", "invalid_version":
					old := map[string]string{"newer": "0.3.0", "numeric_newer": "0.10.0", "invalid_version": "01.0.0"}[change]
					command, want = filepath.Join(packages, old, "peerturn"), "mcp_registration_failed"
				case "foreign_parent":
					command, want = filepath.Join(filepath.Dir(packages), "foreign", "0.1.0", "peerturn"), "mcp_registration_failed"
				case "custom_layout":
					root = filepath.Join(filepath.Dir(packages), "foreign", "0.2.0")
					command, want = filepath.Join(filepath.Dir(packages), "foreign", "0.1.0", "peerturn"), "mcp_registration_failed"
				case "relative":
					command, want = "0.1.0/peerturn", "mcp_registration_failed"
				case "unclean":
					command, want = packages+"/foreign/../0.1.0/peerturn", "mcp_registration_failed"
				case "args", "env", "policy", "query_failed":
					want = "mcp_registration_failed"
				}
				var entry, transport map[string]any
				if host == "codex" {
					entry = codexMCPEntry(t, root)
					var ok bool
					transport, ok = entry["transport"].(map[string]any)
					if !ok {
						t.Fatal("invalid fixture")
					}
					transport["env_vars"] = []string{"XDG_RUNTIME_DIR"}
				} else {
					entry = map[string]any{"type": "stdio", "args": []string{"mcp", "--current"}, "env": map[string]string{}}
					transport = entry
				}
				transport["command"] = command
				switch change {
				case "args":
					transport["args"] = []string{"mcp", "--session", "foreign"}
				case "env":
					transport["env"] = map[string]string{"OWNER_SETTING": "private-marker"}
				case "policy":
					entry["enabled_tools"] = []string{"peer_status"}
				}
				data, err := json.Marshal(entry)
				if err != nil {
					t.Fatal(err)
				}
				path := filepath.Join(config, ".claude.json")
				var original []byte
				if host == "claude" {
					writeSetupMCPJSON(t, path, map[string]any{"mcpServers": map[string]any{"peerturn": entry}})
					original = readSetupMCPUserConfig(t, config)
				}
				var calls []string
				invoke := func(_ context.Context, _ string, args []string, output io.Writer) error {
					call := strings.Join(args, " ")
					calls = append(calls, call)
					switch call {
					case "mcp list --json":
						_, writeErr := io.WriteString(output, `[{"name":"peerturn"}]`)
						return writeErr
					case "mcp get peerturn", "mcp get peerturn --json":
						if change == "query_failed" {
							return setupHostExit(1)
						}
						_, writeErr := output.Write(data)
						return writeErr
					default:
						t.Fatalf("existing entry caused a mutation: %v", args)
						return nil
					}
				}
				err = registerSetupMCP(t.Context(), host, root, invoke)
				if want == "" {
					if err != nil {
						t.Fatal(err)
					}
				} else {
					problem, typed := errors.AsType[*localipc.Error](err)
					if !typed || problem.Code != want || strings.Contains(problem.Message, "private-marker") {
						t.Fatalf("wrong typed result: %v; want %s", err, want)
					}
					if want == "mcp_upgrade_required" {
						step := host + " mcp remove peerturn"
						if host == "claude" {
							step += " --scope user"
						}
						if !strings.Contains(problem.Message, step) || !strings.Contains(problem.Message, "rerun peerturn setup "+host) {
							t.Fatalf("upgrade lacks exact owner steps: %s", problem.Message)
						}
					}
				}
				if host == "codex" && !slices.Equal(calls, []string{"mcp list --json", "mcp get peerturn --json"}) {
					t.Fatalf("unexpected Codex calls: %v", calls)
				}
				if host == "claude" {
					preserved := readSetupMCPUserConfig(t, config)
					if !slices.Equal(preserved, original) {
						t.Fatal("native user entry changed")
					}
					if want == "mcp_upgrade_required" && !slices.Equal(calls, []string{"mcp get peerturn"}) {
						t.Fatalf("upgrade lacked native corroboration: %v", calls)
					}
				}
			})
		}
	}
}

func readSetupMCPUserConfig(t *testing.T, config string) []byte {
	t.Helper()
	directory, err := os.OpenRoot(config)
	if err != nil {
		t.Fatal(err)
	}
	data, readErr := directory.ReadFile(".claude.json")
	closeErr := directory.Close()
	if readErr != nil || closeErr != nil {
		t.Fatalf("read native fixture: %v %v", readErr, closeErr)
	}
	return data
}
