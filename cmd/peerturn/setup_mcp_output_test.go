package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/temirlankasen/peerturn/internal/localipc"
)

func TestSetupMCPSubprocessOutputCannotBecomeAbsenceAfterOverflow(t *testing.T) {
	for _, host := range []string{"claude", "codex"} {
		t.Run(host, func(t *testing.T) {
			root, pin, _ := setupObservedFixture(t, host)
			t.Setenv("SETUP_MCP_GET_OUTPUT", strings.Repeat(" ", 65536)+absentSetupMCPMessage(host))
			if host == "codex" {
				t.Setenv("SETUP_MCP_LIST_OUTPUT", strings.Repeat(" ", 65536)+"[]")
			}
			calls := t.TempDir()
			t.Setenv("SETUP_CALLS", filepath.Join(calls, "calls"))
			var stdout, stderr bytes.Buffer
			code := runSetup([]string{host, "--path", root, "--sha256", pin}, &stdout, &stderr)
			var result struct{ Error *localipc.Error }
			if code == 0 || json.Unmarshal(stdout.Bytes(), &result) != nil || result.Error == nil || result.Error.Code != "mcp_registration_failed" {
				t.Fatalf("overflow accepted as absence: code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
			}
			directory, err := os.OpenRoot(calls)
			if err != nil {
				t.Fatal(err)
			}
			data, readErr := directory.ReadFile("calls")
			closeErr := directory.Close()
			if readErr != nil || closeErr != nil || strings.Contains(string(data), "mcp add") {
				t.Fatalf("overflow caused MCP write: calls=%s read=%v close=%v", data, readErr, closeErr)
			}
		})
	}
}
