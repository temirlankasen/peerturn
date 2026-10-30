package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"strings"
	"testing"

	"github.com/temirlankasen/peerturn/internal/daemon"
	"github.com/temirlankasen/peerturn/internal/localipc"
)

func TestClientBindingAndStdinValidation(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	t.Setenv("CLAUDE_CODE_MESSAGING_SOCKET", "")
	t.Setenv("CLAUDE_CODE_MESSAGING_TOKEN", "")
	t.Setenv("CODEX_THREAD_ID", "")
	for _, test := range []struct {
		name  string
		args  []string
		input string
		code  int
	}{
		{"current needs binding", []string{"invite", "create", "--current", "--scope", "coauthor"}, "", 3},
		{"send requires stdin", []string{"send", "--session", "chosen", "--peer", "p", "--kind", "coauthor"}, "", 2},
		{"join rejects argv link", []string{"join", "--session", "chosen", "--link", "secret"}, "", 2},
		{"invalid scope", []string{"send", "--session", "chosen", "--peer", "p", "--kind", "owner", "--stdin"}, "body", 2},
		{"ask requires stdin", []string{"send", "--session", "chosen", "--peer", "p", "--kind", "ask"}, "body", 2},
		{"oversized body", []string{"send", "--session", "chosen", "--peer", "p", "--kind", "coauthor", "--stdin"}, strings.Repeat("a", 16*1024+1), 2},
		{"empty invite", []string{"join", "--session", "chosen", "--scope", "coauthor", "--link-stdin"}, "\n", 2},
		{"no implicit key mode", []string{"init", "--relay", "https://relay.invalid", "--session", "chosen"}, "", 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if got := runInput(test.args, strings.NewReader(test.input), &stdout, &stderr); got != test.code {
				t.Fatalf("code=%d stdout=%s stderr=%s", got, stdout.String(), stderr.String())
			}
			if strings.Contains(stderr.String(), "secret") || strings.Contains(stdout.String(), "secret") {
				t.Fatal("argument secret leaked")
			}
		})
	}
}

func TestCLIExplicitTextAndSecretLinkStayInIPC(t *testing.T) {
	t.Setenv("TMPDIR", "/tmp")
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	dir, err := daemon.DefaultRuntimeDir()
	if err != nil {
		t.Fatal(err)
	}
	server, err := localipc.Listen(dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	requests := make(chan localipc.Request, 2)
	go func() {
		done <- server.Serve(ctx, func(_ context.Context, request localipc.Request) (any, error) {
			requests <- request
			return map[string]string{"state": "stored"}, nil
		})
	}()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
		if err := server.Close(); err != nil {
			t.Error(err)
		}
	})
	var stdout, stderr bytes.Buffer
	args := []string{"send", "--session", "chosen", "--peer", "p", "--kind", "coauthor", "--stdin"}
	text := "literal @path https://example.invalid\n"
	if code := runInput(args, strings.NewReader(text), &stdout, &stderr); code != 0 {
		t.Fatalf("send code=%d %s", code, stderr.String())
	}
	if request := <-requests; request.Body != text || request.Session != "chosen" {
		t.Fatalf("request = %#v", request)
	}
	args = []string{"join", "--session", "chosen", "--scope", "coauthor", "--link-stdin"}
	stdout.Reset()
	stderr.Reset()
	if code := runInput(args, strings.NewReader(testInvitationLink()+"\n"), &stdout, &stderr); code != 0 {
		t.Fatalf("join code=%d %s", code, stderr.String())
	}
	if request := <-requests; request.Method != "join.text" || request.InvitationText != testInvitationLink()+"\n" || request.Link != "" {
		t.Fatal("CLI did not keep the full invitation text in private IPC")
	}
	if strings.Contains(stdout.String()+stderr.String(), "#v=1") {
		t.Fatal("secret link leaked")
	}
}

func TestInitRelayPinForwarding(t *testing.T) {
	pin := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0x42}, 32))
	args := []string{"init", "--relay", "https://relay.invalid", "--relay-key", pin, "--session", "chosen", "--file-permissions"}
	command, err := parseCommand(args)
	if err != nil {
		t.Fatal(err)
	}
	options := initializationOptions(command, t.TempDir())
	if options.PinnedRelayKey != pin {
		t.Fatal("explicit relay pin was not forwarded to daemon initialization")
	}
}

func TestInitRejectsInvalidRelayPin(t *testing.T) {
	const invalidPin = "invalid-relay-key-value"
	args := []string{"init", "--relay", "https://relay.invalid", "--relay-key", invalidPin, "--session", "chosen", "--file-permissions", "--state-dir", t.TempDir()}
	var stdout, stderr bytes.Buffer
	if code := runInput(args, strings.NewReader(""), &stdout, &stderr); code != 2 {
		t.Fatalf("exit=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if strings.Contains(stdout.String()+stderr.String(), invalidPin) {
		t.Fatal("invalid pin leaked in diagnostics")
	}
}
