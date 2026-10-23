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

func TestSetupRefusesUnsupportedRuntimeBeforeEffects(t *testing.T) {
	for _, host := range []string{"claude", "codex"} {
		for _, mode := range []string{"newer", "malformed", "oversized", "failed"} {
			t.Run(host+"/"+mode, func(t *testing.T) {
				root, pin, _ := setupObservedFixture(t, host)
				qualified, detected, output := "2.1.306", "2.1.308", "2.1.308 (Claude Code)"
				if host == "codex" {
					qualified, detected, output = "0.163.0", "0.164.0", "codex-cli 0.164.0"
				}
				switch mode {
				case "malformed":
					output, detected = "/private/sensitive-path token=private-marker", "unknown"
				case "oversized":
					output, detected = strings.Repeat("private-marker", 6000), "unknown"
				case "failed":
					t.Setenv("SETUP_VERSION_EXIT", "1")
					detected = "unknown"
				}
				t.Setenv("SETUP_VERSION", output)
				t.Setenv("SETUP_VERSION_STDERR", "/private/sensitive-path token=private-marker\n")
				calls := filepath.Join(t.TempDir(), "calls")
				t.Setenv("SETUP_CALLS", calls)
				var stdout, stderr bytes.Buffer
				code := runSetup([]string{host, "--path", root, "--sha256", pin}, &stdout, &stderr)
				var result struct{ Error *localipc.Error }
				if json.Unmarshal(stdout.Bytes(), &result) != nil || result.Error == nil || result.Error.Code != "unsupported_version" || code != 6 {
					t.Fatalf("runtime refusal missing: code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
				}
				for _, want := range []string{qualified, detected, "PATH", "setup " + host} {
					if !strings.Contains(result.Error.Message, want) {
						t.Fatalf("refusal has no actionable %q: %s", want, result.Error.Message)
					}
				}
				callLog, err := os.OpenRoot(filepath.Dir(calls))
				if err != nil {
					t.Fatal(err)
				}
				data, readErr := callLog.ReadFile("calls")
				closeErr := callLog.Close()
				if readErr != nil || closeErr != nil || string(data) != "--version\n" {
					t.Fatalf("refusal ran mutating host commands: %q, read=%v close=%v", data, readErr, closeErr)
				}
				combined := stdout.String() + stderr.String()
				if len(combined) > 2048 || strings.Contains(combined, "private-marker") || strings.Contains(combined, "sensitive-path") || strings.Contains(combined, "reload-plugins") {
					t.Fatal("refusal leaked host output or printed activation steps")
				}
			})
		}
	}
}
