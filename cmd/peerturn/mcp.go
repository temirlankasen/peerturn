package main

import (
	"context"
	"io"
	"os"

	"github.com/temirlankasen/peerturn/internal/daemon"
	"github.com/temirlankasen/peerturn/internal/localipc"
	"github.com/temirlankasen/peerturn/internal/mcp"
)

func runMCP(ctx context.Context, command cliCommand, stdin io.Reader, stdout, stderr io.Writer) int {
	var bindingEpoch string
	if command.flags["current"] {
		session, epoch, err := currentMCPBinding(ctx, command)
		if err != nil {
			return writeMCPFailure(stderr, err)
		}
		if explicit := command.values["session"]; explicit != "" && explicit != session {
			return writeMCPFailure(stderr, localipc.NewError("binding_mismatch", "needs_rebind"))
		}
		command.values["session"] = session
		bindingEpoch = epoch
	}
	thread := os.Getenv("CODEX_THREAD_ID")
	if command.values["session"] == "" {
		command.values["session"] = thread
	}
	if command.values["session"] != thread {
		thread = ""
	}
	if command.values["session"] == "" {
		return writeMCPFailure(stderr, localipc.NewError("unbound", "unbound"))
	}
	dir, err := daemon.DefaultRuntimeDir()
	if err == nil {
		err = mcp.Serve(ctx, stdin, stdout, mcp.Options{Session: command.values["session"], Caller: localipc.Client{RuntimeDir: dir, NativeThread: thread, BindingEpoch: bindingEpoch}, ExpectedBindingEpoch: bindingEpoch})
	}
	if err != nil {
		return writeMCPFailure(stderr, err)
	}
	return 0
}
