package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/temirlankasen/peerturn/internal/daemon"
	"github.com/temirlankasen/peerturn/internal/localipc"
)

type mcpRuntimeInput struct {
	steps []func() string
	part  *strings.Reader
}

func (r *mcpRuntimeInput) Read(data []byte) (int, error) {
	for {
		if r.part != nil {
			n, err := r.part.Read(data)
			if err != io.EOF {
				return n, err
			}
		}
		if len(r.steps) == 0 {
			return 0, io.EOF
		}
		r.part = strings.NewReader(r.steps[0]())
		r.steps = r.steps[1:]
	}
}

func mcpRuntimeState(t *testing.T) string {
	t.Helper()
	t.Setenv("TMPDIR", "/tmp")
	t.Setenv("XDG_RUNTIME_DIR", "")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("CODEX_THREAD_ID", "")
	t.Setenv("CLAUDE_CODE_MESSAGING_SOCKET", "")
	t.Setenv("CLAUDE_CODE_MESSAGING_TOKEN", "")
	state, err := daemon.DefaultStateDir()
	if err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(state, 0700); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(state, "runtime.path")
}

func mcpRuntimePointer(t *testing.T, pointer, runtime string) {
	t.Helper()
	if err := os.WriteFile(pointer, []byte(runtime+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
}

func mcpRuntimeDaemon(t *testing.T) (string, func() []localipc.Request) {
	t.Helper()
	base, err := makeMCPRuntimeDirectory("/tmp", "pt-mcp-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if removeErr := os.RemoveAll(base); removeErr != nil {
			t.Error(removeErr)
		}
	})
	calls := serveHook(t, func(request localipc.Request) (any, error) {
		if request.Method == "hello" {
			return map[string]string{"capability": "synthetic-late-runtime-capability"}, nil
		}
		return map[string]string{"state": "late_runtime_active"}, nil
	}, base)
	runtime, err := daemon.DefaultRuntimeDir()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_RUNTIME_DIR", "")
	return runtime, calls
}

// Test names exceed macOS's Unix socket path limit; keep the parent bounded.
func makeMCPRuntimeDirectory(base, pattern string) (string, error) {
	return os.MkdirTemp(base, pattern)
}

func exposeMCPRuntimeFixture(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	// Deliberately violate private permissions to exercise fail-closed reads.
	mode := info.Mode().Perm() | 0044
	if info.IsDir() {
		mode |= 0011
	}
	return os.Chmod(path, mode)
}

func TestMCPRuntimeRecoversAfterDaemonAppears(t *testing.T) {
	pointer := mcpRuntimeState(t)
	discovery, tool, ok := strings.Cut(codexMCPFixture(t), `{"jsonrpc":"2.0","id":3`)
	if !ok {
		t.Fatal("fixture has no tool request")
	}
	tool = `{"jsonrpc":"2.0","id":3` + tool
	var calls func() []localipc.Request
	input := &mcpRuntimeInput{steps: []func() string{
		func() string { return discovery + tool },
		func() string {
			var runtime string
			runtime, calls = mcpRuntimeDaemon(t)
			mcpRuntimePointer(t, pointer, runtime)
			return strings.Replace(tool, `"id":3`, `"id":4`, 1)
		},
	}}
	var stdout, stderr bytes.Buffer
	code := runInput([]string{"mcp", "--current"}, input, &stdout, &stderr)
	if code != 0 || stderr.Len() != 0 || calls == nil {
		t.Fatalf("MCP stopped before late daemon: code=%d stderr=%s", code, stderr.String())
	}
	var responses []map[string]json.RawMessage
	decoder := json.NewDecoder(&stdout)
	for range 4 {
		var response map[string]json.RawMessage
		if err := decoder.Decode(&response); err != nil {
			t.Fatal(err)
		}
		responses = append(responses, response)
	}
	if !bytes.Contains(responses[1]["result"], []byte(`"peer_status"`)) ||
		!bytes.Contains(responses[2]["result"], []byte(`"isError":true`)) ||
		!bytes.Contains(responses[3]["result"], []byte("late_runtime_active")) ||
		bytes.Contains(responses[3]["result"], []byte(`"isError":true`)) {
		t.Fatalf("same MCP connection did not recover: first=%s next=%s", responses[2]["result"], responses[3]["result"])
	}
	requests := calls()
	if len(requests) != 3 || requests[0].Method != "hello" || !requests[0].Lease || requests[1].Method != "status" || requests[2].Method != "bye" {
		t.Fatalf("late runtime lost capability lease: %+v", requests)
	}
	for _, request := range requests {
		if request.Session != hookThread || request.NativeThread != hookThread {
			t.Fatal("late discovery changed the selected native session")
		}
	}
}

func TestMCPRuntimeRejectsPointerChangedAfterDiscovery(t *testing.T) {
	for _, hostile := range []string{"symlink", "permissions", "relative", "oversized", "target_symlink", "target_permissions"} {
		t.Run(hostile, func(t *testing.T) {
			pointer := mcpRuntimeState(t)
			runtime, calls := mcpRuntimeDaemon(t)
			mcpRuntimePointer(t, pointer, runtime)
			discovery, tool, ok := strings.Cut(codexMCPFixture(t), `{"jsonrpc":"2.0","id":3`)
			if !ok {
				t.Fatal("fixture has no tool request")
			}
			input := &mcpRuntimeInput{steps: []func() string{
				func() string { return discovery },
				func() string {
					var err error
					switch hostile {
					case "symlink":
						if err = os.Remove(pointer); err == nil {
							err = os.Symlink(filepath.Join(runtime, "private-marker"), pointer)
						}
					case "permissions":
						err = exposeMCPRuntimeFixture(pointer)
					case "relative":
						mcpRuntimePointer(t, pointer, "relative/private-marker")
					case "oversized":
						mcpRuntimePointer(t, pointer, "/"+strings.Repeat("private-marker", 400))
					case "target_symlink":
						link := filepath.Join(t.TempDir(), "runtime-link")
						err = os.Symlink(runtime, link)
						mcpRuntimePointer(t, pointer, link)
					case "target_permissions":
						err = exposeMCPRuntimeFixture(runtime)
					}
					if err != nil {
						t.Fatal(err)
					}
					return `{"jsonrpc":"2.0","id":3` + tool
				},
			}}
			var stdout, stderr bytes.Buffer
			code := runInput([]string{"mcp", "--current"}, input, &stdout, &stderr)
			if code != 0 || len(calls()) != 0 || !strings.Contains(stdout.String(), "unsafe_runtime") || !strings.Contains(stdout.String(), `"isError":true`) {
				t.Fatalf("hostile pointer was bypassed: code=%d calls=%d stdout=%s stderr=%s", code, len(calls()), stdout.String(), stderr.String())
			}
			if strings.Contains(stdout.String()+stderr.String(), "private-marker") {
				t.Fatal("runtime refusal disclosed pointer contents")
			}
		})
	}
}

func TestMCPRuntimeRejectsValidPointerChangeAfterCapability(t *testing.T) {
	pointer := mcpRuntimeState(t)
	first, firstCalls := mcpRuntimeDaemon(t)
	second, secondCalls := mcpRuntimeDaemon(t)
	mcpRuntimePointer(t, pointer, first)
	discovery, tool, ok := strings.Cut(codexMCPFixture(t), `{"jsonrpc":"2.0","id":3`)
	if !ok {
		t.Fatal("fixture has no tool request")
	}
	tool = `{"jsonrpc":"2.0","id":3` + tool
	input := &mcpRuntimeInput{steps: []func() string{
		func() string { return discovery + tool },
		func() string {
			mcpRuntimePointer(t, pointer, second)
			return strings.Replace(tool, `"id":3`, `"id":4`, 1)
		},
	}}
	var stdout, stderr bytes.Buffer
	code := runInput([]string{"mcp", "--current"}, input, &stdout, &stderr)
	if code != 0 || stderr.Len() != 0 || !strings.Contains(stdout.String(), "needs_rebind") || !strings.Contains(stdout.String(), `"isError":true`) {
		t.Fatalf("changed valid runtime did not require rebind: code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if requests := secondCalls(); len(requests) != 0 {
		t.Fatalf("changed runtime received capability or handshake: %+v", requests)
	}
	requests := firstCalls()
	if len(requests) != 3 || requests[0].Method != "hello" || requests[1].Method != "status" || requests[2].Method != "bye" {
		t.Fatalf("original runtime lost capability cleanup: %+v", requests)
	}
}
