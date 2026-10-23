package main

import (
	"bytes"
	"context"
	"io"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func absentSetupMCPMessage(host string) string {
	if host == "codex" {
		return "Error: No MCP server named 'peerturn' found."
	}
	return "No MCP server named \"peerturn\". Run `claude mcp add` to add one."
}

func absentSetupMCP(host string, output io.Writer) error {
	if _, err := io.WriteString(output, absentSetupMCPMessage(host)); err != nil {
		return err
	}
	return setupHostExit(1)
}

func TestSetupRegistersCurrentMCPAndPrintsVerification(t *testing.T) {
	for _, host := range []string{"claude", "codex"} {
		t.Run(host, func(t *testing.T) {
			root, _, prefix := setupObservedFixture(t, host)
			marketplaces, plugins := observedSetupJSON(t, host, root, filepath.Join(root, prefix))
			var registrations [][]string
			invoke := func(_ context.Context, _ string, args []string, output io.Writer) error {
				var text string
				switch strings.Join(args, " ") {
				case "--version":
					text = "2.1.306 (Claude Code)"
					if host == "codex" {
						text = "codex-cli 0.163.0"
					}
				case "plugin marketplace list --json":
					text = marketplaces
				case "plugin list --json":
					text = plugins
				case "mcp list --json":
					text = "[]"
				case "mcp get peerturn", "mcp get peerturn --json":
					return absentSetupMCP(host, output)
				}
				if len(args) >= 2 && args[0] == "mcp" && args[1] == "add" {
					registrations = append(registrations, slices.Clone(args))
				}
				_, err := io.WriteString(output, text)
				return err
			}
			var stdout bytes.Buffer
			if err := installSetup(context.Background(), host, root, &stdout, invoke); err != nil {
				t.Fatal(err)
			}
			want := []string{"mcp", "add", "peerturn", "--", filepath.Join(root, "peerturn"), "mcp", "--current"}
			if host == "claude" {
				want = []string{"mcp", "add", "--transport", "stdio", "--scope", "user", "peerturn", "--", filepath.Join(root, "peerturn"), "mcp", "--current"}
			}
			if len(registrations) != 1 || !slices.Equal(registrations[0], want) {
				t.Fatalf("MCP not registered with exact executable/current binding: %v", registrations)
			}
			for _, step := range []string{"/mcp", "peer_status", "peer_join", "not proof", "same session"} {
				if !strings.Contains(stdout.String(), step) {
					t.Fatalf("missing verification step %q: %s", step, stdout.String())
				}
			}
			if host == "codex" {
				for _, step := range []string{`env_vars = ["XDG_RUNTIME_DIR"]`, "On Linux", "runtime_unavailable", "never saves the variable's value"} {
					if !strings.Contains(stdout.String(), step) {
						t.Fatalf("missing Linux runtime instruction %q", step)
					}
				}
			}
		})
	}
}

func TestSetupMCPRegistrationFailureDoesNotClaimSuccess(t *testing.T) {
	for _, host := range []string{"claude", "codex"} {
		t.Run(host, func(t *testing.T) {
			root, pin, _ := setupObservedFixture(t, host)
			t.Setenv("SETUP_MCP_EXIT", "1")
			var stdout, stderr bytes.Buffer
			if code := runSetup([]string{host, "--path", root, "--sha256", pin}, &stdout, &stderr); code == 0 {
				t.Fatal("setup ignored failed MCP registration")
			}
			if strings.Contains(stdout.String(), "peer_status") || strings.Contains(stdout.String(), "reload-plugins") {
				t.Fatal("setup printed normal verification/activation after registration failure")
			}
		})
	}
}
