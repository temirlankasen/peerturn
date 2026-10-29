package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/temirlankasen/peerturn/internal/localipc"
)

// A real child gives the synthetic native socket owner an actual ancestor role.
func TestCurrentCommandFixtureProcess(t *testing.T) {
	if os.Getenv("PEERTURN_TEST_CURRENT_PROCESS") != "1" {
		return
	}
	for i, arg := range os.Args {
		if arg == "--" {
			os.Exit(runInput(os.Args[i+1:], os.Stdin, os.Stdout, os.Stderr))
		}
	}
	os.Exit(2)
}

func currentCommandProcess(t *testing.T, args []string, input string) (int, string, string) {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	// Invoke only this test binary; never start an installed agent runtime.
	command := exec.CommandContext(t.Context(), "/peerturn-test-fixture")
	command.Path, command.Err = executable, nil
	command.Args = append([]string{executable, "-test.run=^TestCurrentCommandFixtureProcess$", "--"}, args...)
	command.Env = append(os.Environ(), "PEERTURN_TEST_CURRENT_PROCESS=1")
	command.Stdin = strings.NewReader(input)
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	err = command.Run()
	if err == nil {
		return 0, stdout.String(), stderr.String()
	}
	if exit, ok := errors.AsType[*exec.ExitError](err); ok {
		return exit.ExitCode(), stdout.String(), stderr.String()
	}
	t.Fatal(err)
	return 0, "", ""
}

func currentNativeFixture(t *testing.T) (string, <-chan error) {
	t.Helper()
	dir := currentNativeDirectory(t)
	socket := filepath.Join(dir, "native.sock")
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
	done := make(chan error, 1)
	go func() {
		connection, acceptErr := listener.AcceptUnix()
		if acceptErr != nil {
			done <- acceptErr
			return
		}
		defer func() {
			if closeErr := connection.Close(); closeErr != nil {
				t.Error(closeErr)
			}
		}()
		data := make([]byte, 1)
		count, readErr := connection.Read(data)
		if count != 0 || !errors.Is(readErr, io.EOF) {
			done <- io.ErrUnexpectedEOF
			return
		}
		done <- nil
	}()
	return socket, done
}

func currentNativeDirectory(t *testing.T) string {
	t.Helper()
	dir, err := makeMCPRuntimeDirectory("/tmp", "pt-current-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if removeErr := os.RemoveAll(dir); removeErr != nil {
			t.Error(removeErr)
		}
	})
	dir, err = filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func assertCurrentNoNativeBytes(t *testing.T, done <-chan error) {
	t.Helper()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal("current resolution wrote to the native model route", err)
		}
	case <-time.After(time.Second):
		t.Fatal("current resolution did not close its read-only native probe")
	}
}

func TestClaudeCurrentDiagnosticsNeverChallenge(t *testing.T) {
	for _, name := range []string{"doctor", "status"} {
		t.Run(name, func(t *testing.T) {
			calls := serveHook(t, func(r localipc.Request) (any, error) {
				if r.Method == "adapter.current" {
					return map[string]string{"session": "chosen", "binding_epoch": strings.Repeat("b", 32)}, nil
				}
				if r.Method == name {
					return map[string]string{"state": "active"}, nil
				}
				return nil, localipc.NewError("needs_rebind", "needs_rebind")
			})
			socket, done := currentNativeFixture(t)
			t.Setenv("CODEX_THREAD_ID", "")
			t.Setenv("CLAUDE_CODE_MESSAGING_SOCKET", socket)
			t.Setenv("CLAUDE_CODE_MESSAGING_TOKEN", "synthetic-private-native-token")
			code, stdout, stderr := currentCommandProcess(t, []string{name, "--current"}, "")
			requests := calls()
			if code != 0 || stderr != "" || len(requests) != 2 || requests[0].Method != "adapter.current" || requests[1].Method != name || requests[1].Session != "chosen" {
				t.Fatalf("read-only current failed: code=%d requests=%+v stdout=%s stderr=%s", code, requests, stdout, stderr)
			}
			assertCurrentNoNativeBytes(t, done)
		})
	}
}

func TestClaudeCurrentProofFailures(t *testing.T) {
	for _, mode := range []string{"unconfirmed", "missing_epoch", "replaced_socket"} {
		t.Run(mode, func(t *testing.T) {
			socket, done := currentNativeFixture(t)
			calls := serveHook(t, func(r localipc.Request) (any, error) {
				if r.Method != "adapter.current" || mode == "unconfirmed" {
					return nil, localipc.NewError("needs_rebind", "needs_rebind")
				}
				epoch := strings.Repeat("b", 32)
				if mode == "missing_epoch" {
					epoch = ""
				}
				if mode == "replaced_socket" {
					if err := os.Remove(socket); err != nil {
						return nil, err
					}
					replacement, err := net.ListenUnix("unix", &net.UnixAddr{Name: socket, Net: "unix"})
					if err != nil {
						return nil, err
					}
					t.Cleanup(func() {
						if closeErr := replacement.Close(); closeErr != nil {
							t.Error(closeErr)
						}
					})
					if err = os.Chmod(socket, 0600); err != nil {
						return nil, err
					}
				}
				return map[string]string{"session": "chosen", "binding_epoch": epoch}, nil
			})
			t.Setenv("CODEX_THREAD_ID", "")
			t.Setenv("CLAUDE_CODE_MESSAGING_SOCKET", socket)
			t.Setenv("CLAUDE_CODE_MESSAGING_TOKEN", "")
			code, _, stderr := currentCommandProcess(t, []string{"doctor", "--current"}, "")
			requests := calls()
			if code != 3 || len(requests) != 1 || requests[0].Method != "adapter.current" || !strings.Contains(stderr, "needs_rebind") {
				t.Fatalf("invalid proof survived: %d %+v %s", code, requests, stderr)
			}
			assertCurrentNoNativeBytes(t, done)
		})
	}
}

func TestClaudeExplicitBindStillChallenges(t *testing.T) {
	nonce := strings.Repeat("a", 64)
	calls := serveHook(t, func(r localipc.Request) (any, error) {
		if r.Method == "adapter.challenge" {
			return map[string]string{"nonce": nonce}, nil
		}
		return map[string]string{"session": "chosen"}, nil
	})
	dir := currentNativeDirectory(t)
	socket := filepath.Join(dir, "native.sock")
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
	done := make(chan bool, 1)
	go func() {
		connection, acceptErr := listener.AcceptUnix()
		if acceptErr != nil {
			done <- false
			return
		}
		defer func() {
			if closeErr := connection.Close(); closeErr != nil {
				t.Error(closeErr)
			}
		}()
		decoder := json.NewDecoder(connection)
		var auth struct{ Type, Token string }
		var frame struct {
			Type    string
			Message struct{ Content string }
		}
		authErr := decoder.Decode(&auth)
		frameErr := decoder.Decode(&frame)
		done <- authErr == nil && frameErr == nil && auth.Type == "auth" && auth.Token == "synthetic-token" && frame.Type == "user" && frame.Message.Content == "<peerturn-bind>"+nonce+"</peerturn-bind>"
	}()
	t.Setenv("CODEX_THREAD_ID", "")
	t.Setenv("CLAUDE_CODE_MESSAGING_SOCKET", socket)
	t.Setenv("CLAUDE_CODE_MESSAGING_TOKEN", "synthetic-token")
	code, _, stderr := currentCommandProcess(t, []string{"session", "bind", "--current"}, "")
	requests := calls()
	if code != 0 || stderr != "" || len(requests) != 2 || requests[0].Method != "adapter.challenge" || requests[1].Method != "adapter.resolve" || !<-done {
		t.Fatalf("explicit bind lost native challenge: %d %+v %s", code, requests, stderr)
	}
}
