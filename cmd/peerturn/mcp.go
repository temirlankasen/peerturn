package main

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"sync"

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
	deferred := command.flags["current"] && command.values["session"] == ""
	if command.values["session"] == "" && !deferred {
		return writeMCPFailure(stderr, localipc.NewError("unbound", "unbound"))
	}
	options := mcp.Options{Session: command.values["session"], Caller: &mcpRuntimeCaller{nativeThread: thread, bindingEpoch: bindingEpoch}, ExpectedBindingEpoch: bindingEpoch, CodexThread: thread}
	if deferred {
		options.ResolveCodex = func(nativeThread string) (mcp.Caller, error) {
			return &mcpRuntimeCaller{nativeThread: nativeThread}, nil
		}
	}
	if err := mcp.Serve(ctx, stdin, stdout, options); err != nil {
		return writeMCPFailure(stderr, err)
	}
	return 0
}

type mcpRuntimeCaller struct {
	mu                              sync.Mutex
	nativeThread, bindingEpoch, dir string
}

func (c *mcpRuntimeCaller) client(releasing bool) (localipc.Client, error) {
	dir := c.dir
	if !releasing || dir == "" {
		var err error
		dir, err = daemon.DefaultRuntimeDir()
		if err != nil {
			return localipc.Client{}, err
		}
		if c.dir != "" && dir != c.dir {
			return localipc.Client{}, localipc.NewError("needs_rebind", "needs_rebind")
		}
	}
	return localipc.Client{RuntimeDir: dir, NativeThread: c.nativeThread, BindingEpoch: c.bindingEpoch}, nil
}

func (c *mcpRuntimeCaller) Call(ctx context.Context, request localipc.Request) (json.RawMessage, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	// Releasing existing authority must use its original endpoint even after
	// discovery changes or becomes unavailable.
	client, err := c.client(request.Method == "bye")
	if err != nil {
		return nil, err
	}
	return client.Call(ctx, request)
}

func (c *mcpRuntimeCaller) OpenCapability(ctx context.Context, session string) (json.RawMessage, func() error, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	client, err := c.client(false)
	if err != nil {
		return nil, nil, err
	}
	result, closeLease, err := client.OpenCapability(ctx, session)
	if err == nil {
		// Hosts may start before the daemon publishes discovery. Keep resolving
		// lazily until hello succeeds, then never move its authority elsewhere.
		c.dir = client.RuntimeDir
	}
	return result, closeLease, err
}
