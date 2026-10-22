// PeerTurn connects explicitly selected coding-agent sessions.
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/temirlankasen/peerturn/internal/relay"
	"github.com/temirlankasen/peerturn/internal/relay/config"
	"github.com/temirlankasen/peerturn/internal/version"
)

const usage = `Usage: peerturn <command>

Commands:
  init [--codex-current] [--relay-key KEY]  Initialize explicit preview or verified current Codex identity
  setup claude|codex [--path DIR --sha256 PIN]  Install a verified local release plugin
  daemon start|stop|status  Control the foreground local daemon
  keys isolate [--state-dir DIR]  Rotate a stopped store into the OS keystore
  invite create --current  Create a coauthor invitation for the native session
  join     Consume a coauthor link from stdin for the selected session
  peers|send|reply|inbox|receipt|status|grants|doctor  Local JSON commands
  mcp --current  Run the seven-tool stdio proxy using verified current-session binding
  session bind --current  Bind the current Claude Code session using its native socket
  session unbind          Disconnect Claude and invalidate its grants
  delivery resolve ID --sent|--not-sent  Reconcile ambiguous delivery (Codex: --sent only)
  adapter claude  Private Claude Code 2.1.306 mod helper
  relay --config FILE  Run the durable ciphertext relay
  version  Show version and build metadata
  help     Show this help

PeerTurn is pre-alpha. Claude Code 2.1.306 and Codex 0.163.0 shared daemon
coauthor/inform adapters are experimental. Ask remains unavailable until isolation
and budget qualification. Codex embedded TUI is unsupported.
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	return runInput(args, os.Stdin, stdout, stderr)
}

func runInput(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) > 0 && args[0] == "setup" {
		return runSetup(args[1:], stdout, stderr)
	}
	if len(args) > 0 && localCommand(args[0]) {
		return runLocal(args, stdin, stdout, stderr)
	}
	if len(args) > 0 && args[0] == "relay" {
		return runRelay(args[1:], stderr)
	}
	output, destination, code := usage, stdout, 0
	switch {
	case len(args) > 1:
		output = "peerturn: expected exactly one command\n\n" + usage
		destination, code = stderr, 2
	case len(args) == 0:
	case args[0] == "help", args[0] == "--help", args[0] == "-h":
	case args[0] == "version":
		output = version.String() + "\n"
	default:
		output = fmt.Sprintf("peerturn: unknown command %q\n\n%s", args[0], usage)
		destination, code = stderr, 2
	}
	if _, err := io.WriteString(destination, output); err != nil {
		return 1
	}
	return code
}

func runRelay(args []string, stderr io.Writer) int {
	if len(args) != 2 || args[0] != "--config" || args[1] == "" {
		if _, err := io.WriteString(stderr, "peerturn: relay requires --config FILE\n\n"+usage); err != nil {
			return 1
		}
		return 2
	}
	cfg, err := config.Load(args[1])
	if err != nil {
		if _, writeErr := io.WriteString(stderr, "peerturn: invalid relay configuration\n"); writeErr != nil {
			return 1
		}
		return 2
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err = relay.Run(ctx, cfg); err != nil {
		if _, writeErr := io.WriteString(stderr, "peerturn: relay unavailable\n"); writeErr != nil {
			return 1
		}
		return 1
	}
	return 0
}
