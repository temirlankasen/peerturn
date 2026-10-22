package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/temirlankasen/peerturn/internal/localipc"
)

func TestMCPCurrentCodexJoinUsesExactNativeBinding(t *testing.T) {
	calls := serveHook(t, func(r localipc.Request) (any, error) {
		if r.Method == "hello" {
			return map[string]string{"capability": "synthetic-bound-capability"}, nil
		}
		return map[string]string{"state": "waiting_inviter"}, nil
	})
	t.Setenv("CODEX_THREAD_ID", hookThread)
	t.Setenv("CLAUDE_CODE_MESSAGING_SOCKET", "")
	t.Setenv("CLAUDE_CODE_MESSAGING_TOKEN", "")
	text := "private-prose-marker\n<" + testInvitationLink() + ">"
	arguments, err := json.Marshal(map[string]string{"text": text})
	if err != nil {
		t.Fatal(err)
	}
	input := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"fixture","version":"1"}}}` + "\n" +
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"peer_join","arguments":` + string(arguments) + "}}\n"
	var stdout, stderr bytes.Buffer
	code := runInput([]string{"mcp", "--current"}, strings.NewReader(input), &stdout, &stderr)
	requests := calls()
	if code != 0 || len(requests) != 3 || stderr.Len() != 0 {
		t.Fatalf("current MCP code=%d request count=%d stderr=%s", code, len(requests), stderr.String())
	}
	for _, request := range requests {
		if request.Session != hookThread || request.NativeThread != hookThread {
			t.Fatal("MCP lost the inherited exact native thread")
		}
	}
	if requests[0].Method != "hello" || requests[1].Method != "join.text" || requests[1].InvitationText != text || requests[1].Capability != "synthetic-bound-capability" || requests[2].Method != "bye" {
		t.Fatal("MCP join bypassed the daemon capability handshake")
	}
	if strings.Contains(stdout.String()+stderr.String(), "private-prose-marker") || strings.Contains(stdout.String()+stderr.String(), "#v=1") {
		t.Fatal("native MCP reflected the invitation")
	}
}

func TestMCPCurrentClaudeEpochGap(t *testing.T) {
	for _, mode := range []string{"same", "replaced", "missing"} {
		t.Run(mode, func(t *testing.T) { checkMCPCurrentClaudeEpoch(t, mode) })
	}
}

func checkMCPCurrentClaudeEpoch(t *testing.T, mode string) {
	t.Helper()
	nonce := strings.Repeat("a", 64)
	oldEpoch, newEpoch := strings.Repeat("b", 32), strings.Repeat("c", 32)
	if mode == "same" {
		newEpoch = oldEpoch
	}
	if mode == "missing" {
		oldEpoch = ""
	}
	calls := serveHook(t, func(r localipc.Request) (any, error) {
		switch r.Method {
		case "adapter.challenge":
			return map[string]string{"nonce": nonce}, nil
		case "adapter.resolve":
			return map[string]string{"session": "chosen", "binding_epoch": oldEpoch}, nil
		case "hello":
			return map[string]string{"capability": "synthetic-new-capability", "binding_epoch": newEpoch}, nil
		}
		return map[string]string{"state": "released"}, nil
	})
	dir := t.TempDir()
	canonical, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	socket := filepath.Join(canonical, "native.sock")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: socket, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if closeErr := listener.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	})
	if err = os.Chmod(socket, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEX_THREAD_ID", "")
	t.Setenv("CLAUDE_CODE_MESSAGING_SOCKET", socket)
	t.Setenv("CLAUDE_CODE_MESSAGING_TOKEN", "synthetic-private-native-token")
	nativeDone := make(chan error, 1)
	go func() {
		connection, acceptErr := listener.AcceptUnix()
		if acceptErr != nil {
			nativeDone <- acceptErr
			return
		}
		defer func() {
			if closeErr := connection.Close(); closeErr != nil {
				return
			}
		}()
		decoder := json.NewDecoder(connection)
		var auth struct{ Type, Token string }
		var frame struct {
			Message struct{ Content string }
		}
		if acceptErr = decoder.Decode(&auth); acceptErr == nil {
			acceptErr = decoder.Decode(&frame)
		}
		if acceptErr == nil && (auth.Token != "synthetic-private-native-token" || frame.Message.Content != "<peerturn-bind>"+nonce+"</peerturn-bind>") {
			acceptErr = errors.New("unexpected synthetic native challenge")
		}
		nativeDone <- acceptErr
	}()
	var stdout, stderr bytes.Buffer
	code := runInput([]string{"mcp", "--current"}, strings.NewReader(""), &stdout, &stderr)
	timer := time.NewTimer(time.Second)
	defer timer.Stop()
	select {
	case err = <-nativeDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-timer.C:
		t.Fatal("native challenge did not complete")
	}
	requests := calls()
	if mode == "same" {
		if code != 0 || stdout.Len() != 0 || stderr.Len() != 0 || len(requests) != 4 || requests[3].Method != "bye" {
			t.Fatalf("verified incarnation could not initialize: code=%d requests=%d stderr=%s", code, len(requests), stderr.String())
		}
		return
	}
	expectedRequests := 4
	if mode == "missing" {
		expectedRequests = 2
	}
	if code != 3 || stdout.Len() != 0 || !strings.Contains(stderr.String(), "needs_rebind") || len(requests) != expectedRequests || (mode == "replaced" && requests[3].Method != "bye") {
		t.Fatalf("startup changed Claude incarnation without fresh proof: code=%d requests=%d stdout=%s stderr=%s", code, len(requests), stdout.String(), stderr.String())
	}
}

func TestMCPCurrentBindingFailures(t *testing.T) {
	for _, mode := range []string{"ambiguous", "foreign", "missing", "claude_missing"} {
		t.Run(mode, func(t *testing.T) {
			calls := serveHook(t, func(localipc.Request) (any, error) {
				return map[string]string{"state": "unexpected"}, nil
			})
			t.Setenv("CODEX_THREAD_ID", hookThread)
			t.Setenv("CLAUDE_CODE_MESSAGING_SOCKET", "")
			t.Setenv("CLAUDE_CODE_MESSAGING_TOKEN", "")
			args := []string{"mcp", "--current"}
			switch mode {
			case "ambiguous":
				t.Setenv("CLAUDE_CODE_MESSAGING_SOCKET", "/synthetic/native.sock")
			case "foreign":
				args = append(args, "--session", "foreign")
			case "missing":
				t.Setenv("CODEX_THREAD_ID", "")
			case "claude_missing":
				t.Setenv("CODEX_THREAD_ID", "")
				t.Setenv("CLAUDE_CODE_MESSAGING_SOCKET", "/synthetic/native.sock")
				t.Setenv("CLAUDE_CODE_MESSAGING_TOKEN", "synthetic-private-native-token")
			}
			var stdout, stderr bytes.Buffer
			code := runInput(args, strings.NewReader(""), &stdout, &stderr)
			if code != 3 || stdout.Len() != 0 || len(calls()) != 0 || strings.Contains(stderr.String(), "synthetic-private-native-token") {
				t.Fatalf("MCP binding failure escaped protocol boundary: code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
			}
		})
	}
}
