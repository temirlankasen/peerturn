package main

import (
	"context"
	"os"

	"github.com/temirlankasen/peerturn/internal/claudeipc"
	"github.com/temirlankasen/peerturn/internal/daemon"
	"github.com/temirlankasen/peerturn/internal/localipc"
)

func currentSession(ctx context.Context, command cliCommand) (string, error) {
	if thread := os.Getenv("CODEX_THREAD_ID"); thread != "" {
		if os.Getenv("CLAUDE_CODE_MESSAGING_SOCKET") != "" || os.Getenv("CLAUDE_CODE_MESSAGING_TOKEN") != "" {
			return "", localipc.NewError("binding_mismatch", "needs_rebind")
		}
		if command.name == "session bind" || command.name == "session unbind" {
			return "", localipc.NewError("unsupported", "unsupported")
		}
		// The daemon verifies this exact thread against its stored native binding;
		// the inherited UUID selects the request, but cannot authorize attachment.
		return thread, nil
	}
	dir, err := daemon.DefaultRuntimeDir()
	if err != nil {
		return "", err
	}
	return claudeipc.BindCurrent(ctx, localipc.Client{RuntimeDir: dir})
}

func currentMCPBinding(ctx context.Context, command cliCommand) (string, string, error) {
	if os.Getenv("CODEX_THREAD_ID") != "" {
		session, err := currentSession(ctx, command)
		return session, "", err
	}
	if os.Getenv("CLAUDE_CODE_MESSAGING_SOCKET") == "" && os.Getenv("CLAUDE_CODE_MESSAGING_TOKEN") == "" {
		// Codex starts MCP from its app-server environment, not a tool child.
		// Only a later host tools/call can select its exact native thread.
		return "", "", nil
	}
	dir, err := daemon.DefaultRuntimeDir()
	if err != nil {
		return "", "", err
	}
	evidence, err := claudeipc.BindCurrentEvidence(ctx, localipc.Client{RuntimeDir: dir})
	return evidence.Session, evidence.BindingEpoch, err
}
