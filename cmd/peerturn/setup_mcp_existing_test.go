package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/temirlankasen/peerturn/internal/localipc"
)

func codexMCPEntry(t *testing.T, root string) map[string]any {
	t.Helper()
	return map[string]any{
		"name": "peerturn", "enabled": true, "disabled_reason": nil,
		"transport": map[string]any{
			"type": "stdio", "command": filepath.Join(root, "peerturn"),
			"args": []string{"mcp", "--current"}, "env": nil, "env_vars": []string{}, "cwd": nil,
		},
		"enabled_tools": nil, "disabled_tools": nil, "startup_timeout_sec": nil, "tool_timeout_sec": nil,
	}
}

func TestSetupCodexMCPExistingEntryNeverReplaced(t *testing.T) {
	for _, change := range []string{"ours", "enabled_tools", "disabled_tools", "empty_enabled_tools", "enabled", "env_vars", "foreign_env_vars", "duplicate_env_vars", "extra_env_vars", "env", "command", "args", "cwd", "approval", "malformed", "duplicate", "oversize", "oversize_tail", "query_failed", "unknown_absence"} {
		t.Run(change, func(t *testing.T) {
			root := t.TempDir()
			entry := codexMCPEntry(t, root)
			transport, ok := entry["transport"].(map[string]any)
			if !ok {
				t.Fatal("invalid fixture")
			}
			switch change {
			case "enabled_tools", "disabled_tools":
				entry[change] = []string{"peer_status"}
			case "empty_enabled_tools":
				entry["enabled_tools"] = []string{}
			case "enabled":
				entry[change] = false
			case "env_vars":
				transport[change] = []string{"XDG_RUNTIME_DIR"}
			case "foreign_env_vars":
				transport["env_vars"] = []string{"CODEX_THREAD_ID"}
			case "duplicate_env_vars":
				transport["env_vars"] = []string{"XDG_RUNTIME_DIR", "XDG_RUNTIME_DIR"}
			case "extra_env_vars":
				transport["env_vars"] = []string{"XDG_RUNTIME_DIR", "OWNER_SETTING"}
			case "env":
				transport[change] = map[string]string{"OWNER_SETTING": "private-marker"}
			case "command", "cwd":
				transport[change] = "/foreign"
			case "args":
				transport[change] = []string{"mcp", "--session", "foreign"}
			case "approval":
				entry["default_tools_approval_mode"] = "prompt"
			}
			data, err := json.Marshal(entry)
			if err != nil {
				t.Fatal(err)
			}
			switch change {
			case "malformed":
				data = []byte("{")
			case "duplicate":
				data = append([]byte(`{"name":"foreign",`), data[1:]...)
			case "oversize":
				data = []byte(strings.Repeat(" ", 65537))
			case "unknown_absence":
				data = []byte("unrecognized failure")
			}
			var calls []string
			invoke := func(_ context.Context, _ string, args []string, output io.Writer) error {
				calls = append(calls, strings.Join(args, " "))
				if calls[len(calls)-1] == "mcp list --json" {
					_, writeErr := io.WriteString(output, `[{"name":"peerturn"}]`)
					return writeErr
				}
				if calls[len(calls)-1] == "mcp get peerturn --json" {
					if change == "oversize_tail" {
						if _, writeErr := output.Write([]byte(absentSetupMCPMessage("codex"))); writeErr != nil {
							return writeErr
						}
						_, writeErr := output.Write([]byte(strings.Repeat("x", 65537)))
						return writeErr
					}
					if _, writeErr := output.Write(data); writeErr != nil {
						return writeErr
					}
					if change == "query_failed" || change == "unknown_absence" {
						return errors.New("host failed")
					}
				}
				return nil
			}
			err = registerSetupMCP(t.Context(), "codex", root, invoke)
			if len(calls) != 2 || calls[0] != "mcp list --json" || calls[1] != "mcp get peerturn --json" {
				t.Fatalf("existing/uncertain entry caused a write: %v", calls)
			}
			if change == "ours" || change == "env_vars" {
				if err != nil {
					t.Fatal(err)
				}
			} else if problem, typed := errors.AsType[*localipc.Error](err); !typed || problem.Code != "mcp_registration_failed" || strings.Contains(problem.Message, "private-marker") {
				t.Fatalf("conflict not safely reported: %v", err)
			}
		})
	}
}

func TestSetupClaudeMCPExistingOrUncertainEntryNeverAdded(t *testing.T) {
	setupMCPHome(t)
	for _, queryFailed := range []bool{false, true} {
		var calls []string
		invoke := func(_ context.Context, _ string, args []string, output io.Writer) error {
			calls = append(calls, strings.Join(args, " "))
			if _, err := io.WriteString(output, "existing or unrecognized configuration"); err != nil {
				return err
			}
			if queryFailed {
				return errors.New("query failed")
			}
			return nil
		}
		err := registerSetupMCP(t.Context(), "claude", t.TempDir(), invoke)
		problem, typed := errors.AsType[*localipc.Error](err)
		if len(calls) != 1 || calls[0] != "mcp get peerturn" || !typed || problem.Code != "mcp_registration_failed" {
			t.Fatalf("Claude entry was not preserved: calls=%v err=%v", calls, err)
		}
	}
}
