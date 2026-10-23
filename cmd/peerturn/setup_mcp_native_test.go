package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

type setupHostExit int

func (e setupHostExit) Error() string { return fmt.Sprintf("host exit %d", e) }
func (e setupHostExit) ExitCode() int { return int(e) }

func setupMCPHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	config := filepath.Join(home, ".claude")
	if err := os.Mkdir(config, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", config)
	t.Setenv("CLAUDE_CODE_PROJECT_DIR_NAME", "")
	t.Chdir(home)
	return config
}

func TestSetupMCPNativeAbsenceDiagnostics(t *testing.T) {
	for _, host := range []string{"claude", "codex"} {
		t.Run(host, func(t *testing.T) {
			directory, err := os.OpenRoot("testdata")
			if err != nil {
				t.Fatal(err)
			}
			data, readErr := directory.ReadFile(host + "-mcp-absence.json")
			closeErr := directory.Close()
			if readErr != nil || closeErr != nil {
				t.Fatalf("read capture: read=%v close=%v", readErr, closeErr)
			}
			var capture struct {
				Exit           int
				Stdout, Stderr string
			}
			if err := json.Unmarshal(data, &capture); err != nil {
				t.Fatal(err)
			}
			setupMCPHome(t)
			var calls []string
			invoke := func(_ context.Context, _ string, args []string, output io.Writer) error {
				command := strings.Join(args, " ")
				calls = append(calls, command)
				if command == "mcp list --json" {
					_, err := io.WriteString(output, "[]\n")
					return err
				}
				if strings.HasPrefix(command, "mcp get ") {
					if _, err := io.WriteString(output, capture.Stdout+capture.Stderr); err != nil {
						return err
					}
					return setupHostExit(capture.Exit)
				}
				return nil
			}
			root := t.TempDir()
			if err := registerSetupMCP(t.Context(), host, root, invoke); err != nil {
				t.Fatalf("captured absence blocked registration: %v", err)
			}
			query := "mcp get peerturn"
			add := "mcp add --transport stdio --scope user peerturn -- "
			if host == "codex" {
				query = "mcp list --json"
				add = "mcp add peerturn -- "
			}
			want := []string{query, add + filepath.Join(root, "peerturn") + " mcp --current"}
			if !slices.Equal(calls, want) {
				t.Fatalf("absence must use native structured/exit signals: got %v want %v", calls, want)
			}
		})
	}
}
