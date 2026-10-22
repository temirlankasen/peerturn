package codex

import (
	"context"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"time"
)

const sandboxReadControl = "peerturn-sandbox-read-control"

// SandboxConfig selects the exact native executable and launch environment.
type SandboxConfig struct {
	Executable  string
	Directory   string
	Environment []string
	Deadline    time.Duration
}

// SandboxReport describes only effective tool write/network canaries. It never
// upgrades the stock file view into authority isolation or qualified ask support.
type SandboxReport struct {
	Platform                 string `json:"platform"`
	Engine                   string `json:"engine"`
	ReadOnly                 bool   `json:"read_only"`
	ToolNetworkOff           bool   `json:"tool_network_off"`
	AuthoritySecretsIsolated bool   `json:"authority_secrets_isolated"`
	QualifiedAsk             bool   `json:"qualified_ask"`
	Reason                   string `json:"reason"`
}

// NativeSandboxPreflight exercises Codex's host sandbox: Seatbelt on macOS or
// bwrap/user namespaces on Linux. It never relaxes host policy or retries outside
// the sandbox. Provider networking is outside this tool-process probe.
func NativeSandboxPreflight(ctx context.Context, cfg SandboxConfig) (report SandboxReport, resultErr error) {
	report.Platform = runtime.GOOS
	switch runtime.GOOS {
	case "darwin":
		report.Engine = "seatbelt"
	case "linux":
		report.Engine = "bwrap_userns"
	default:
		return report, &ResponderError{Reason: "sandbox_unavailable"}
	}
	if !filepath.IsAbs(cfg.Executable) || !filepath.IsAbs(cfg.Directory) {
		return report, &ResponderError{Reason: "sandbox_unavailable"}
	}
	environment, err := responderEnvironment(cfg.Environment)
	if err != nil {
		return report, err
	}
	if cfg.Deadline == 0 {
		cfg.Deadline = 15 * time.Second
	}
	if cfg.Deadline <= 0 || cfg.Deadline > 30*time.Second {
		return report, &ResponderError{Reason: "sandbox_unavailable"}
	}
	ctx, cancel := context.WithTimeout(ctx, cfg.Deadline)
	defer cancel()
	directory, err := os.MkdirTemp("", "peerturn-sandbox-")
	if err != nil {
		return report, &ResponderError{Reason: "sandbox_unavailable"}
	}
	defer func() {
		if removeErr := os.RemoveAll(directory); removeErr != nil {
			report = SandboxReport{}
			resultErr = &ResponderError{Reason: "sandbox_unavailable"}
		}
	}()
	readPath, writePath := filepath.Join(directory, "read-control"), filepath.Join(directory, "write-canary")
	if err = os.WriteFile(readPath, []byte(sandboxReadControl), 0600); err != nil {
		return report, &ResponderError{Reason: "sandbox_unavailable"}
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return report, &ResponderError{Reason: "sandbox_unavailable"}
	}
	var connections atomic.Int64
	server := &http.Server{ReadHeaderTimeout: time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }),
		ConnState: func(_ net.Conn, state http.ConnState) {
			if state == http.StateNew {
				connections.Add(1)
			}
		}}
	served := make(chan error, 1)
	go func() { served <- server.Serve(listener) }()
	defer func() {
		closeErr := server.Close()
		serveErr := <-served
		if closeErr != nil || (serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed)) {
			report = SandboxReport{}
			resultErr = &ResponderError{Reason: "sandbox_unavailable"}
		}
	}()
	run := func(command []string, sandbox bool) ([]byte, error) {
		launch := ResponderCommand{Executable: command[0], Args: command[1:], Directory: cfg.Directory, Environment: environment}
		if sandbox {
			launch.Executable = cfg.Executable
			launch.Args = append([]string{"-c", `sandbox_mode="read-only"`, "-c", `approval_policy="never"`, "sandbox", "--"}, command...)
		}
		return runResponderProcess(ctx, launch, 16<<10)
	}
	network := []string{"/usr/bin/curl", "--silent", "--show-error", "--fail", "--max-time", "2", "--noproxy", "*", "http://" + listener.Addr().String() + "/"}
	if _, err = run(network, false); err != nil {
		return report, &ResponderError{Reason: "sandbox_unavailable"}
	}
	// A writable control proves the fixture and touch executable work on this host.
	if _, err = run([]string{"/usr/bin/touch", writePath}, false); err != nil {
		return report, &ResponderError{Reason: "sandbox_unavailable"}
	}
	if err = os.Remove(writePath); err != nil {
		return report, &ResponderError{Reason: "sandbox_unavailable"}
	}
	connections.Store(0)
	probe := sandboxCanaries{
		read: []string{"/bin/cat", readPath}, write: []string{"/usr/bin/touch", writePath}, network: network,
		run:             func(ctx context.Context, command []string) ([]byte, error) { return run(command, true) },
		exists:          func() bool { _, statErr := os.Lstat(writePath); return !errors.Is(statErr, os.ErrNotExist) },
		networkObserved: func() bool { return connections.Load() > 0 },
	}
	if err = probe.check(ctx); err != nil {
		return report, err
	}
	report.ReadOnly = true
	report.ToolNetworkOff = true
	report.Reason = "protected_path_policy_failed"
	return report, nil
}

type sandboxCanaries struct {
	read, write, network []string
	run                  func(context.Context, []string) ([]byte, error)
	exists               func() bool
	networkObserved      func() bool
}

func (p sandboxCanaries) check(ctx context.Context) error {
	failure := func() error { return &ResponderError{Reason: "sandbox_unavailable"} }
	read, err := p.run(ctx, p.read)
	if err != nil || string(read) != sandboxReadControl {
		return failure()
	}
	if _, err = p.run(ctx, p.write); err == nil || p.exists() {
		return failure()
	}
	if _, err = p.run(ctx, p.network); err == nil {
		return failure()
	}
	if p.networkObserved != nil && p.networkObserved() {
		return failure()
	}
	if ctx.Err() != nil || p.exists() {
		return failure()
	}
	return nil
}
