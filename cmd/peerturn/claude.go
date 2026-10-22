package main

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/temirlankasen/peerturn/internal/claudeipc"
	"github.com/temirlankasen/peerturn/internal/daemon"
	"github.com/temirlankasen/peerturn/internal/localipc"
)

func runClaudeAdapter(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "claude" {
		return writeFailure(stdout, stderr, invalidArguments())
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if len(args) == 2 && args[1] == "owner-boundary" {
		// The classic hook has no trusted human-origin field. The mod supplies
		// an inbox notice only on a host-verified composer/bridge prompt event.
		_, err := io.Copy(io.Discard, io.LimitReader(stdin, 64*1024))
		if err != nil {
			return 1
		}
		if err = json.NewEncoder(stdout).Encode(map[string]any{}); err != nil {
			return 1
		}
		return 0
	}
	if len(args) != 7 {
		return writeFailure(stdout, stderr, invalidArguments())
	}
	values := map[string]string{}
	for i := 1; i < len(args); i += 2 {
		if args[i] != "--native-session" && args[i] != "--runtime-version" && args[i] != "--provider" {
			return writeFailure(stdout, stderr, invalidArguments())
		}
		if values[args[i]] != "" {
			return writeFailure(stdout, stderr, invalidArguments())
		}
		values[args[i]] = args[i+1]
	}
	dir, err := daemon.DefaultRuntimeDir()
	if err != nil {
		return writeMCPFailure(stderr, err)
	}
	err = claudeipc.Serve(ctx, stdout, claudeipc.Options{NativeSession: values["--native-session"], RuntimeVersion: values["--runtime-version"], Provider: values["--provider"], Client: localipc.Client{RuntimeDir: dir}})
	if err != nil {
		return writeMCPFailure(stderr, err)
	}
	return 0
}
