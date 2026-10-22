package main

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/temirlankasen/peerturn/internal/daemon"
	"github.com/temirlankasen/peerturn/internal/localipc"
	"github.com/temirlankasen/peerturn/internal/relay/auth"
)

func runLocal(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if args[0] == "adapter" {
		return runClaudeAdapter(args[1:], stdin, stdout, stderr)
	}
	if args[0] == "codex-hook" && len(args) == 1 {
		return runCodexHook(stdin, stdout)
	}
	command, err := parseCommand(args)
	if err != nil {
		if args[0] == "mcp" {
			return writeMCPFailure(stderr, err)
		}
		return writeFailure(stdout, stderr, err)
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if command.name == "mcp" {
		return runMCP(ctx, command, stdin, stdout, stderr)
	}
	if command.flags["current"] {
		session, bindErr := currentSession(ctx, command)
		if bindErr != nil {
			return writeFailure(stdout, stderr, bindErr)
		}
		if explicit := command.values["session"]; explicit != "" && explicit != session {
			return writeFailure(stdout, stderr, localipc.NewError("binding_mismatch", "needs_rebind"))
		}
		command.values["session"] = session
		if command.name == "session bind" {
			if err = json.NewEncoder(stdout).Encode(map[string]string{"session": session, "binding": "native_claude"}); err != nil {
				return 1
			}
			return 0
		}
		if command.values["scope"] == "" && (command.name == "join" || command.name == "invite create") {
			command.values["scope"] = "coauthor"
		}
	}
	if command.name == "init" {
		return runInit(ctx, command, stdout, stderr)
	}
	if command.name == "keys isolate" {
		return runKeyIsolation(ctx, command, stdout, stderr)
	}
	if command.name == "daemon start" {
		return runDaemon(ctx, command, stdout, stderr)
	}
	request, err := command.request(stdin)
	if err != nil {
		return writeFailure(stdout, stderr, err)
	}
	dir, err := daemon.DefaultRuntimeDir()
	if err != nil {
		return writeFailure(stdout, stderr, err)
	}
	client := localipc.Client{RuntimeDir: dir, NativeThread: request.NativeThread}
	result, err := client.Call(ctx, request)
	if err != nil {
		return writeFailure(stdout, stderr, err)
	}
	var delivered []string
	if request.Method == "inbox" {
		delivered, err = localipc.InformDeliveryIDs(result)
		if err != nil {
			return writeFailure(stdout, stderr, err)
		}
	}
	result = append(result, '\n')
	if n, writeErr := stdout.Write(result); writeErr != nil || n != len(result) {
		return 1
	}
	for _, id := range delivered {
		if _, err = client.Call(ctx, localipc.Request{Method: "inbox.delivered", Session: request.Session, MessageID: id}); err != nil {
			return writeMCPFailure(stderr, localipc.NewError("unavailable", "pending"))
		}
	}
	return 0
}

func stateDir(command cliCommand) (string, error) {
	if dir := command.values["state-dir"]; dir != "" {
		return dir, nil
	}
	return daemon.ConfiguredStateDir()
}

func runInit(ctx context.Context, command cliCommand, stdout, stderr io.Writer) int {
	if command.flags["codex-current"] {
		thread := os.Getenv("CODEX_THREAD_ID")
		if thread == "" {
			return writeFailure(stdout, stderr, localipc.NewError("unbound", "unbound"))
		}
		if selected := command.values["session"]; selected != "" && selected != thread {
			return writeFailure(stdout, stderr, localipc.NewError("binding_mismatch", "unbound"))
		}
		command.values["session"] = thread
	}
	if command.values["relay"] == "" || command.values["session"] == "" || !command.flags["file-permissions"] {
		return writeFailure(stdout, stderr, invalidArguments())
	}
	if pin := command.values["relay-key"]; pin != "" && !auth.ValidRoutingKey(pin) {
		return writeFailure(stdout, stderr, invalidArguments())
	}
	dir, err := stateDir(command)
	if err != nil {
		return writeFailure(stdout, stderr, err)
	}
	options := initializationOptions(command, dir)
	if command.values["state-dir"] == "" {
		options.ConfigPath, err = daemon.DefaultConfigPath()
	} else {
		options.ConfigPath = filepath.Join(dir, "config.toml")
	}
	if err != nil {
		return writeFailure(stdout, stderr, err)
	}
	status, err := daemon.Initialize(ctx, options)
	if err != nil {
		return writeFailure(stdout, stderr, err)
	}
	if err := json.NewEncoder(stdout).Encode(status); err != nil {
		return 1
	}
	return 0
}

func initializationOptions(command cliCommand, dir string) daemon.InitOptions {
	o := daemon.InitOptions{StateDir: dir, RelayURL: command.values["relay"], SessionLabel: command.values["session"], FilePermissions: command.flags["file-permissions"], PinnedRelayKey: command.values["relay-key"]}
	if command.flags["codex-current"] {
		o.CodexThreadID = command.values["session"]
	}
	return o
}

func runDaemon(ctx context.Context, command cliCommand, stdout, stderr io.Writer) int {
	dir, err := stateDir(command)
	if err != nil {
		return writeFailure(stdout, stderr, err)
	}
	configPath := filepath.Join(dir, "config.toml")
	if command.values["state-dir"] == "" {
		configPath, err = daemon.DefaultConfigPath()
		if err != nil {
			return writeFailure(stdout, stderr, err)
		}
	}
	if err := daemon.Run(ctx, daemon.Options{StateDir: dir, ConfigPath: configPath}); err != nil {
		return writeFailure(stdout, stderr, err)
	}
	if err := json.NewEncoder(stdout).Encode(map[string]string{"state": "stopped"}); err != nil {
		return 1
	}
	return 0
}

func writeMCPFailure(stderr io.Writer, err error) int {
	if _, writeErr := io.WriteString(stderr, "peerturn: "+localipc.PublicError(err).Code+"\n"); writeErr != nil {
		return 1
	}
	return localipc.ExitCode(err)
}

func writeFailure(stdout, stderr io.Writer, err error) int {
	public := localipc.PublicError(err)
	if writeErr := json.NewEncoder(stdout).Encode(map[string]*localipc.Error{"error": public}); writeErr != nil {
		return 1
	}
	if _, writeErr := io.WriteString(stderr, "peerturn: "+public.Code+"\n"); writeErr != nil {
		return 1
	}
	return localipc.ExitCode(err)
}
