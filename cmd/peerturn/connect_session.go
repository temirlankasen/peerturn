package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/temirlankasen/peerturn/internal/claudeipc"
	"github.com/temirlankasen/peerturn/internal/daemon"
	"github.com/temirlankasen/peerturn/internal/localipc"
	"github.com/temirlankasen/peerturn/internal/relay/auth"
)

func (c connector) requireClaudeHost() error {
	thread := c.getenv("CODEX_THREAD_ID")
	socket, token := c.getenv("CLAUDE_CODE_MESSAGING_SOCKET"), c.getenv("CLAUDE_CODE_MESSAGING_TOKEN")
	switch {
	case thread != "" && (socket != "" || token != ""):
		return localipc.NewError("binding_mismatch", "needs_rebind")
	case thread != "":
		return connectError("unsupported", "unsupported", "connect binds a Claude Code session. For Codex use peerturn init --codex-current with its documented MCP flow.")
	case socket == "" || token == "" || !filepath.IsAbs(socket):
		return connectError("unbound", "unbound", "Run connect from the selected Claude Code session's tool child: its native messaging environment is missing.")
	}
	return nil
}

func (c connector) resolveRelay(requested string) (daemon.Config, bool, error) {
	requested = strings.TrimSuffix(requested, "/")
	if requested != "" && !validRelayURL(requested) {
		return daemon.Config{}, false, connectError("invalid_arguments", "rejected", "--relay must be an https origin without path, query, fragment or credentials.")
	}
	config, err := c.readConfig()
	switch {
	case err == nil:
		if requested != "" && requested != config.Relay {
			return daemon.Config{}, false, connectError("conflict", "rejected", "The existing profile uses a different relay. Nothing was changed; omit --relay or use the stored one.")
		}
		return config, true, nil
	case errors.Is(err, os.ErrNotExist):
		if requested == "" {
			return daemon.Config{}, false, connectError("invalid_arguments", "rejected", "First use needs --relay https://HOST to initialize the local profile.")
		}
		return daemon.Config{Relay: requested, StorageMode: "file_permissions"}, false, nil
	default:
		return daemon.Config{}, false, err
	}
}

func validRelayURL(value string) bool {
	parsed, err := url.Parse(value)
	return err == nil && parsed.Scheme == "https" && auth.ValidAuthority(parsed.Host) && parsed.User == nil &&
		parsed.Opaque == "" && parsed.Path == "" && parsed.RawPath == "" && parsed.RawQuery == "" && !parsed.ForceQuery && parsed.Fragment == ""
}

func (c connector) status(ctx context.Context) (daemon.Status, error) {
	raw, err := c.call(ctx, localipc.Request{Method: "status"})
	if err != nil {
		return daemon.Status{}, err
	}
	var status daemon.Status
	if json.Unmarshal(raw, &status) != nil {
		return daemon.Status{}, localipc.NewError("invalid_response", "unavailable")
	}
	return status, nil
}

func (c connector) daemonRunning(ctx context.Context) (bool, error) {
	_, err := c.status(ctx)
	switch {
	case err == nil:
		return true, nil
	case errorCode(err) == "unavailable":
		return false, nil
	default:
		return false, err
	}
}

// Setup must never replace another native session's binding.
func (c connector) ensureBinding(ctx context.Context) (claudeipc.CurrentEvidence, string, error) {
	evidence, err := c.resolve(ctx)
	if err == nil {
		return evidence, "reused", nil
	}
	if errorCode(err) != "needs_rebind" {
		return claudeipc.CurrentEvidence{}, "", err
	}
	status, err := c.status(ctx)
	if err != nil {
		return claudeipc.CurrentEvidence{}, "", err
	}
	if status.Binding == "native_claude" {
		return claudeipc.CurrentEvidence{}, "", connectError("needs_rebind", "needs_rebind", "An active native binding exists that this session cannot prove as its own. connect never replaces it; unbind it explicitly or resume the owning session.")
	}
	session, err := c.bindOnce(ctx, status.Binding == "needs_rebind")
	if err != nil {
		return claudeipc.CurrentEvidence{}, "", err
	}
	evidence, err = c.resolve(ctx)
	if err != nil {
		return claudeipc.CurrentEvidence{}, "", err
	}
	if evidence.Session != session {
		return claudeipc.CurrentEvidence{}, "", localipc.NewError("binding_mismatch", "needs_rebind")
	}
	return evidence, "bound", nil
}

func (c connector) bindOnce(ctx context.Context, pinned bool) (string, error) {
	session, err := c.bind(ctx)
	if err == nil || errorCode(err) != "needs_rebind" {
		return session, err
	}
	if pinned {
		return "", connectError("needs_rebind", "needs_rebind", "This profile is pinned to a Claude session that is not connected now. Resume that session, or run peerturn session unbind from it, before connecting another; connect never changes the pin.")
	}
	return "", connectError("needs_rebind", "needs_rebind", "The native helper has not registered with the daemon yet. Reload or resume this session so its helper reconnects, then run peerturn connect again.")
}
