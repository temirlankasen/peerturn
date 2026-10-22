package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/temirlankasen/peerturn/internal/localipc"
)

func TestReleasedCapabilitiesDoNotExhaustMCPStarts(t *testing.T) {
	d, _ := pairedStores(t)
	for range 256 {
		result, err := d.Dispatch(t.Context(), localipc.Request{Method: "hello", Session: d.state.Label})
		if err != nil {
			t.Fatalf("MCP start failed after earlier proxy exits: %v", err)
		}
		var binding struct {
			Capability string `json:"capability"`
		}
		decodeResult(t, result, &binding)
		request := localipc.Request{Method: "bye", Session: d.state.Label, Capability: binding.Capability}
		if _, err := d.Dispatch(t.Context(), request); err != nil {
			t.Fatalf("MCP capability cannot be released: %v", err)
		}
		request.Method = "status"
		if _, err := d.Dispatch(t.Context(), request); localipc.PublicError(err).Code != "unbound" {
			t.Fatalf("released capability still authorizes requests: %v", err)
		}
	}
}

func TestExpiredCapabilitiesAreRejectedAndDoNotConsumeBudget(t *testing.T) {
	d, _ := pairedStores(t)
	for i := range 128 {
		d.capabilities[string(rune(i))] = capabilityBinding{session: d.state.Label, expiresAt: time.Now().Add(-time.Second)}
	}
	if _, err := d.Dispatch(t.Context(), localipc.Request{Method: "status", Session: d.state.Label, Capability: string(rune(1))}); localipc.PublicError(err).Code != "unbound" {
		t.Fatalf("expired capability still grants authority: %v", err)
	}
	if _, err := d.Dispatch(t.Context(), localipc.Request{Method: "hello", Session: d.state.Label}); err != nil {
		t.Fatalf("expired capabilities retained start budget: %v", err)
	}
}

func TestCapabilityLeaseEndsOnIPCDisconnect(t *testing.T) {
	d, client := capabilityIPCFixture(t)
	for range 130 {
		result, release, err := client.OpenCapability(t.Context(), d.state.Label)
		if err != nil {
			t.Fatalf("new proxy could not acquire released slot: %v", err)
		}
		var binding struct {
			Capability string `json:"capability"`
		}
		if err := json.Unmarshal(result, &binding); err != nil {
			t.Fatal(err)
		}
		request := localipc.Request{Method: "status", Session: d.state.Label, Capability: binding.Capability}
		if _, err := d.Dispatch(t.Context(), request); err != nil {
			t.Fatalf("live proxy lost its capability: %v", err)
		}
		if err := release(); err != nil {
			t.Fatal(err)
		}
		awaitCapabilityRelease(t, d, binding.Capability)
	}
}

func TestLiveCapabilityLeaseStillHasIdleExpiry(t *testing.T) {
	d, client := capabilityIPCFixture(t)
	result, release, err := client.OpenCapability(t.Context(), d.state.Label)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := release(); err != nil {
			t.Error(err)
		}
	}()
	var hello struct {
		Capability string `json:"capability"`
	}
	if err := json.Unmarshal(result, &hello); err != nil {
		t.Fatal(err)
	}
	d.mu.Lock()
	binding := d.capabilities[hello.Capability]
	d.mu.Unlock()
	if binding.expiresAt.IsZero() {
		t.Fatal("a live socket created a capability without expiry")
	}
	binding.expiresAt = time.Now().Add(-time.Second)
	d.mu.Lock()
	d.capabilities[hello.Capability] = binding
	d.mu.Unlock()
	if _, err := d.Dispatch(t.Context(), localipc.Request{Method: "status", Session: d.state.Label, Capability: hello.Capability}); localipc.PublicError(err).Code != "unbound" {
		t.Fatalf("idle capability survived expiry because its socket remained open: %v", err)
	}
}

func TestCapabilityLeaseEndsWhenProxyProcessExits(t *testing.T) {
	d, client := capabilityIPCFixture(t)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	command, err := proxyFixtureProcess(ctx, executable)
	if err != nil {
		t.Fatal(err)
	}
	command.Env = append(os.Environ(), "PEERTURN_CAPABILITY_HELPER="+client.RuntimeDir, "PEERTURN_CAPABILITY_SESSION="+d.state.Label)
	stdin, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	var binding struct {
		Capability string `json:"capability"`
	}
	if err := json.NewDecoder(stdout).Decode(&binding); err != nil {
		cancel()
		if waitErr := command.Wait(); waitErr == nil {
			t.Error("failed proxy startup returned success")
		}
		t.Fatal(err)
	}
	if _, err := d.Dispatch(t.Context(), localipc.Request{Method: "status", Session: d.state.Label, Capability: binding.Capability}); err != nil {
		t.Fatalf("helper never obtained a live capability: %v", err)
	}
	if err := command.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if err := command.Wait(); err == nil {
		t.Fatal("killed proxy returned success")
	}
	if err := stdin.Close(); err != nil && !errors.Is(err, os.ErrClosed) {
		t.Fatal(err)
	}
	awaitCapabilityRelease(t, d, binding.Capability)
}

func proxyFixtureProcess(ctx context.Context, executable string) (*exec.Cmd, error) {
	// Fixture executables come from os.Executable; no PATH lookup or shell is
	// permitted when restarting the test image to exercise process death.
	if !filepath.IsAbs(executable) || filepath.Clean(executable) != executable {
		return nil, localipc.NewError("invalid_encoding", "rejected")
	}
	return exec.CommandContext(ctx, executable, "-test.run=^TestCapabilityProxyHelper$"), nil
}

func TestCapabilityProxyHelper(t *testing.T) {
	runtimeDir := os.Getenv("PEERTURN_CAPABILITY_HELPER")
	if runtimeDir == "" {
		return
	}
	result, release, err := (localipc.Client{RuntimeDir: runtimeDir}).OpenCapability(t.Context(), os.Getenv("PEERTURN_CAPABILITY_SESSION"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stdout.Write(append(result, '\n')); err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(io.Discard, os.Stdin); err != nil {
		t.Fatal(err)
	}
	if err := release(); err != nil {
		t.Fatal(err)
	}
}

func capabilityIPCFixture(t *testing.T) (*Daemon, localipc.Client) {
	t.Helper()
	d, _ := pairedStores(t)
	dir := filepath.Join(filepath.Dir(d.options.StateDir), "runtime")
	server, err := localipc.Listen(dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- server.Serve(ctx, d.Dispatch) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
		if err := server.Close(); err != nil {
			t.Error(err)
		}
	})
	return d, localipc.Client{RuntimeDir: dir}
}

func awaitCapabilityRelease(t *testing.T, d *Daemon, capability string) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for {
		d.mu.Lock()
		_, exists := d.capabilities[capability]
		d.mu.Unlock()
		if !exists {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("closed proxy retained daemon authority")
		}
		time.Sleep(time.Millisecond)
	}
}
