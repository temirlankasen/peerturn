package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/temirlankasen/peerturn/internal/localipc"
)

func TestSetupRefusesUnsupportedRuntimeBeforeEffects(t *testing.T) {
	for _, host := range []string{"claude", "codex"} {
		for _, mode := range []string{"older", "malformed", "oversized", "failed"} {
			t.Run(host+"/"+mode, func(t *testing.T) {
				root, pin, _ := setupObservedFixture(t, host)
				qualified, detected, output := "2.1.306", "2.1.305", "2.1.305 (Claude Code)"
				if host == "codex" {
					qualified, detected, output = "0.163.0", "0.162.0", "codex-cli 0.162.0"
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
				expectedCode := "unsupported_version"
				if mode != "older" {
					expectedCode = "runtime_version_unknown"
				}
				if json.Unmarshal(stdout.Bytes(), &result) != nil || result.Error == nil || result.Error.Code != expectedCode || code != 6 {
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

func TestSetupRuntimeReplacementRefusedBeforeEffects(t *testing.T) {
	root, pin, _ := setupObservedFixture(t, "claude")
	binary, err := exec.LookPath("claude")
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
	program, err := directory.ReadFile(filepath.Base(binary))
	if err != nil {
		t.Fatal(err)
	}
	updated := strings.Replace(string(program), "--version) ", "--version) printf '\\n# updater replacement\\n' >> \"$0\"; ", 1)
	if err = directory.WriteFile(filepath.Base(binary), []byte(updated), 0600); err != nil {
		t.Fatal(err)
	}
	calls := filepath.Join(t.TempDir(), "calls")
	t.Setenv("SETUP_CALLS", calls)
	var stdout, stderr bytes.Buffer
	if code := runSetup([]string{"claude", "--path", root, "--sha256", pin}, &stdout, &stderr); code != 6 || !strings.Contains(stdout.String(), "runtime_contract_changed") {
		t.Fatalf("changed executable accepted: %d %s %s", code, &stdout, &stderr)
	}
	callDirectory, err := os.OpenRoot(filepath.Dir(calls))
	if err != nil {
		t.Fatal(err)
	}
	data, err := callDirectory.ReadFile(filepath.Base(calls))
	if closeErr := callDirectory.Close(); closeErr != nil {
		t.Error(closeErr)
	}
	if err != nil || string(data) != "--version\n" {
		t.Fatalf("mutation after replacement: %s %v", data, err)
	}
}

func TestSetupReportsNewerRuntimeAndCapabilities(t *testing.T) {
	for _, host := range []string{"claude", "codex"} {
		t.Run(host, func(t *testing.T) {
			root, pin, _ := setupObservedFixture(t, host)
			nativeVersion, output := "2.1.308", "2.1.308 (Claude Code)"
			if host == "codex" {
				nativeVersion, output = "0.164.0", "codex-cli 0.164.0"
			}
			t.Setenv("SETUP_VERSION", output)
			var stdout, stderr bytes.Buffer
			if code := runSetup([]string{host, "--path", root, "--sha256", pin}, &stdout, &stderr); code != 0 {
				t.Fatalf("newer setup refused: %d %s %s", code, &stdout, &stderr)
			}
			for _, want := range []string{host, nativeVersion, "newer_unverified", "capabilities", "not_observed"} {
				if !strings.Contains(stdout.String(), want) {
					t.Errorf("missing %q in %s", want, &stdout)
				}
			}
		})
	}
}
