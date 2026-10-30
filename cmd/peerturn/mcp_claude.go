package main

import (
	"context"

	"github.com/temirlankasen/peerturn/internal/claudeipc"
	"github.com/temirlankasen/peerturn/internal/daemon"
	"github.com/temirlankasen/peerturn/internal/localipc"
	"github.com/temirlankasen/peerturn/internal/mcp"
)

func resolveClaudeMCPBinding(ctx context.Context, command cliCommand) (mcp.CurrentBinding, error) {
	dir, err := daemon.DefaultRuntimeDir()
	if err != nil {
		return mcp.CurrentBinding{}, err
	}
	evidence, err := claudeipc.ResolveCurrentEvidence(ctx, localipc.Client{RuntimeDir: dir})
	if err != nil {
		return mcp.CurrentBinding{}, err
	}
	if explicit := command.values["session"]; explicit != "" && explicit != evidence.Session {
		return mcp.CurrentBinding{}, localipc.NewError("binding_mismatch", "needs_rebind")
	}
	return mcp.CurrentBinding{
		Session: evidence.Session, BindingEpoch: evidence.BindingEpoch,
		Caller: &mcpRuntimeCaller{bindingEpoch: evidence.BindingEpoch, dir: dir},
	}, nil
}
