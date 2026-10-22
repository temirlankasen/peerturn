package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/temirlankasen/peerturn/internal/localipc"
)

func TestUnifiedCLICurrentCodex(t *testing.T) {
	calls := serveHook(t, func(localipc.Request) (any, error) {
		return map[string]string{"state": "stored"}, nil
	})
	t.Setenv("CODEX_THREAD_ID", hookThread)
	t.Setenv("CLAUDE_CODE_MESSAGING_SOCKET", "")
	t.Setenv("CLAUDE_CODE_MESSAGING_TOKEN", "")
	var stdout, stderr bytes.Buffer
	code := runInput([]string{"invite", "create", "--current"}, strings.NewReader(""), &stdout, &stderr)
	requests := calls()
	if code != 0 || len(requests) != 1 {
		t.Fatalf("current code=%d requests=%+v stderr=%s", code, requests, stderr.String())
	}
	r := requests[0]
	if r.Method != "invite" || r.Session != hookThread || r.NativeThread != hookThread || r.Scope != "coauthor" {
		t.Fatalf("current request=%+v", r)
	}
}

func TestUnifiedCLICodexCurrentCannotUnbindClaudeSession(t *testing.T) {
	calls := serveHook(t, func(localipc.Request) (any, error) {
		return map[string]string{"state": "unbound"}, nil
	})
	t.Setenv("CODEX_THREAD_ID", hookThread)
	t.Setenv("CLAUDE_CODE_MESSAGING_SOCKET", "")
	t.Setenv("CLAUDE_CODE_MESSAGING_TOKEN", "")
	var stdout, stderr bytes.Buffer
	code := runInput([]string{"session", "unbind", "--current"}, strings.NewReader(""), &stdout, &stderr)
	if code != 6 || !strings.Contains(stderr.String(), "unsupported") || len(calls()) != 0 {
		t.Fatalf("Codex entered Claude unbind: code=%d stdout=%s stderr=%s", code, &stdout, &stderr)
	}
}

func TestUnifiedCLICurrentMismatch(t *testing.T) {
	for _, mode := range []string{"ambiguous", "foreign"} {
		t.Run(mode, func(t *testing.T) {
			calls := serveHook(t, func(localipc.Request) (any, error) {
				return map[string]string{"state": "stored"}, nil
			})
			t.Setenv("CODEX_THREAD_ID", hookThread)
			t.Setenv("CLAUDE_CODE_MESSAGING_SOCKET", "")
			t.Setenv("CLAUDE_CODE_MESSAGING_TOKEN", "")
			args := []string{"status", "--current"}
			if mode == "ambiguous" {
				t.Setenv("CLAUDE_CODE_MESSAGING_SOCKET", "/synthetic/native.sock")
			} else {
				args = append(args, "--session", "foreign")
			}
			var stdout, stderr bytes.Buffer
			code := runInput(args, strings.NewReader(""), &stdout, &stderr)
			if code != 3 || !strings.Contains(stdout.String(), "binding_mismatch") || len(calls()) != 0 {
				t.Fatalf("code=%d requests=%+v output=%s %s", code, calls(), stdout.String(), stderr.String())
			}
		})
	}
}

func TestUnifiedCLIInboxAckKeepsCodexBinding(t *testing.T) {
	writer := &cliInboxWriter{mode: "ok"}
	calls := serveHook(t, func(r localipc.Request) (any, error) {
		if r.Method == "inbox.delivered" {
			if !writer.written.Load() {
				t.Error("inbox acknowledged before complete output")
			}
			return map[string]bool{"delivered": true}, nil
		}
		return map[string]any{"messages": []map[string]string{{"message_id": strings.Repeat("a", 32), "routing": "inbox_no_wake"}}}, nil
	})
	t.Setenv("CODEX_THREAD_ID", hookThread)
	var stderr bytes.Buffer
	code := runInput([]string{"inbox", "--json"}, strings.NewReader(""), writer, &stderr)
	requests := calls()
	if code != 0 || len(requests) != 2 {
		t.Fatalf("code=%d requests=%+v stderr=%s", code, requests, stderr.String())
	}
	for _, r := range requests {
		if r.Session != hookThread || r.NativeThread != hookThread {
			t.Fatalf("lost exact native binding: %+v", r)
		}
	}
}

type shortHookWriter struct{}

func (shortHookWriter) Write(p []byte) (int, error) { return len(p) - 1, nil }

func TestCodexHookShortOutputNeverAcknowledges(t *testing.T) {
	calls := serveHook(t, func(localipc.Request) (any, error) { return hookOffer(), nil })
	executeHook(t, hookInput(), shortHookWriter{})
	if requests := calls(); len(requests) != 1 {
		t.Fatalf("short notice output acknowledged: %+v", requests)
	}
}
