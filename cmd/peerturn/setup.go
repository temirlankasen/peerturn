package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/temirlankasen/peerturn/internal/localipc"
	"github.com/temirlankasen/peerturn/internal/runtimepolicy"
	"github.com/temirlankasen/peerturn/internal/version"
)

type setupInvoke func(context.Context, string, []string, io.Writer) error

func runSetup(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || (args[0] != "claude" && args[0] != "codex") {
		return writeFailure(stdout, stderr, invalidArguments())
	}
	flags := flag.NewFlagSet("setup", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	root := flags.String("path", "", "Previously verified release marketplace root")
	pin := flags.String("sha256", "", "Trusted SHA-256 of .peerturn-files.sha256")
	verbose := flags.Bool("verbose", false, "Print the full activation contract")
	if err := flags.Parse(args[1:]); err != nil || flags.NArg() != 0 {
		return writeFailure(stdout, stderr, invalidArguments())
	}
	if *root == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return writeFailure(stdout, stderr, invalidArguments())
		}
		*root = filepath.Join(home, ".local", "share", "peerturn", strings.TrimPrefix(version.Version, "v"))
		if *pin == "" {
			data, readErr := os.ReadFile(filepath.Join(*root, ".peerturn-verified"))
			if readErr != nil || len(data) > 65 {
				return writeFailure(stdout, stderr, invalidArguments())
			}
			*pin = strings.TrimSpace(string(data))
		}
	}
	absolute, err := resolveSetupRoot(*root)
	if err != nil {
		return writeFailure(stdout, stderr, err)
	}
	if verifySetupTree(absolute, *pin) != nil || verifyMarketplace(absolute, args[0]) != nil {
		return writeFailure(stdout, stderr, invalidArguments())
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	binary, err := exec.LookPath(args[0])
	if err != nil {
		return writeFailure(stdout, stderr, setupRuntimeFailure(args[0], "unknown"))
	}
	binary, err = filepath.Abs(binary)
	if err != nil {
		return writeFailure(stdout, stderr, setupRuntimeFailure(args[0], "unknown"))
	}
	binary, err = filepath.EvalSymlinks(binary)
	if err != nil {
		return writeFailure(stdout, stderr, setupRuntimeFailure(args[0], "unknown"))
	}
	identity, err := setupExecutableInfo(binary)
	if err != nil {
		return writeFailure(stdout, stderr, setupRuntimeFailure(args[0], "unknown"))
	}
	checkIdentity := func() error {
		current, statErr := setupExecutableInfo(binary)
		if statErr != nil || !os.SameFile(identity, current) || identity.Size() != current.Size() || !identity.ModTime().Equal(current.ModTime()) {
			return runtimepolicy.ContractFailure(args[0], "", "executable.identity", "setup", "setup")
		}
		return nil
	}
	invoke := func(ctx context.Context, name string, argv []string, output io.Writer) error {
		if name != args[0] {
			return invalidArguments()
		}
		if identityErr := checkIdentity(); identityErr != nil {
			return localipc.RuntimeError(identityErr)
		}
		var command *exec.Cmd
		switch name {
		case "claude":
			command = exec.CommandContext(ctx, "claude")
		case "codex":
			command = exec.CommandContext(ctx, "codex")
		default:
			return invalidArguments()
		}
		// The canonical path replaces CommandContext's redundant PATH lookup,
		// including its error if an updater has since removed the original link.
		command.Path, command.Err = binary, nil
		command.Args = append([]string{binary}, argv...)
		command.Stdout, command.Stderr, command.Stdin = output, stderr, os.Stdin
		if len(argv) == 1 && argv[0] == "--version" {
			// A version probe is diagnostic data, never host output for the owner.
			command.Stderr, command.Stdin = io.Discard, nil
		}
		if len(argv) >= 2 && argv[0] == "mcp" {
			// Only structured stdout and exit codes inform registration. Native
			// diagnostics may contain private configuration or environment values.
			command.Stderr, command.Stdin = io.Discard, nil
		}
		if commandErr := command.Run(); commandErr != nil {
			return commandErr
		}
		if identityErr := checkIdentity(); identityErr != nil {
			return localipc.RuntimeError(identityErr)
		}
		return nil
	}
	if err = installSetup(ctx, args[0], absolute, stdout, invoke, *verbose); err != nil {
		if public, ok := errors.AsType[*localipc.Error](err); ok {
			return writeFailure(stdout, stderr, public)
		}
		if _, writeErr := io.WriteString(stderr, "peerturn: host plugin setup failed; inspect host output before retrying\n"); writeErr != nil {
			return 1
		}
		return 1
	}
	return 0
}

func verifyMarketplace(root, host string) error {
	manifest := filepath.Join(".claude-plugin", "marketplace.json")
	if host == "codex" {
		manifest = filepath.Join(".agents", "plugins", "marketplace.json")
	}
	directory, err := os.OpenRoot(root)
	if err != nil {
		return err
	}
	data, readErr := readSetupFile(directory, manifest, 64*1024)
	closeErr := directory.Close()
	if readErr != nil || closeErr != nil {
		return invalidArguments()
	}
	var metadata struct{ Name string }
	if json.Unmarshal(data, &metadata) != nil || metadata.Name != "peerturn" {
		return invalidArguments()
	}
	return nil
}

func installSetup(ctx context.Context, host, root string, stdout io.Writer, invoke setupInvoke, verbose bool) error {
	root, err := resolveSetupRoot(root)
	if err != nil {
		return err
	}
	runtime, err := inspectSetupRuntime(ctx, host, invoke)
	if err != nil {
		return err
	}
	// Refuse an existing foreign or unobservable marketplace before host
	// mutation; path aliases alone are the same local installation.
	marketplace, err := inspectSetupMarketplace(ctx, host, root, invoke, true)
	if err != nil {
		return err
	}
	// PeerTurn's own stale marketplace blocks a native add of this release, so
	// setup removes it; the host also uninstalls its plugin and saved options,
	// which setup reinstalls below. A foreign source never reaches this point.
	if marketplace.stale {
		if host == "claude" {
			if err = checkSetupClaudeEnabled(ctx, invoke); err != nil {
				return err
			}
		}
		if err = invoke(ctx, host, []string{"plugin", "marketplace", "remove", "peerturn"}, stdout); err != nil {
			return err
		}
	}
	// A marketplace added by native plugin commands stays the owner's choice;
	// setup adopts it only after its files matched the verified release.
	if !marketplace.published {
		if err = invoke(ctx, host, []string{"plugin", "marketplace", "add", root}, stdout); err != nil {
			return err
		}
		if err = observeSetupMarketplace(ctx, host, root, invoke); err != nil {
			return err
		}
	}
	verb := "install"
	if host == "codex" {
		verb = "add"
	}
	installArgs := []string{"plugin", verb, "peerturn@peerturn"}
	if host == "claude" {
		installArgs = append(installArgs, "--config", "peerturn_binary="+filepath.Join(root, "peerturn"))
	}
	if err = invoke(ctx, host, installArgs, stdout); err != nil {
		return err
	}
	if host == "claude" {
		if err = invoke(ctx, host, []string{"plugin", "update", "peerturn@peerturn"}, stdout); err != nil {
			return err
		}
	}
	if err = observeSetupPlugin(ctx, host, root, marketplace, invoke); err != nil {
		return err
	}
	if err = registerSetupMCP(ctx, host, root, invoke); err != nil {
		return err
	}
	return writeSetupSummary(stdout, host, root, runtime, verbose)
}

// Five lines say what was installed and what to do next; the long contract
// text that nobody read in practice stays behind --verbose.
func writeSetupSummary(stdout io.Writer, host, root string, runtime runtimepolicy.Decision, verbose bool) error {
	lines := []string{
		fmt.Sprintf("PeerTurn %s: %s %s (verification_status=%s, minimum %s); plugin files matched the verified release at %s (a local integrity match, not independent publisher authentication); MCP registered. Available capabilities: setup; live binding and hook contracts: not_observed until activation in the session.", version.Version, runtime.Runtime, runtime.Version, runtime.Status, runtime.Minimum, root),
	}
	if host == "claude" {
		lines = append(lines,
			"Next: in the same session you want to connect, run /reload-plugins once and accept hook trust for PeerTurn when asked; then run peerturn connect from that session. It starts the daemon, binds the session and prints an invitation link. Sessions already open keep the previous plugin until /reload-plugins.",
			"Check: peer_status with {} must show binding native_claude and owner_invitation_hook active before you paste a link; peer_join with {} then accepts it. A registration line or a tool listing is not proof of binding; /mcp reconnect is needed only when MCP was bound to an earlier epoch.")
	} else {
		lines = append(lines,
			"Next: restart or resume the same session and accept hook trust for PeerTurn, then run peerturn init --codex-current with the relay options from its tool child. Shared daemon only; the embedded Codex TUI is unsupported.",
			"Check: peer_status with {} must show binding codex_shared_daemon with capabilities.attach_existing=true; peer_join with {} accepts a pasted link. A tool listing is not proof of binding; reconnect in /mcp only after the daemon is up.",
			`On Linux, MCP can discover the daemon through its private-state runtime pointer. Name-only forwarding is an alternative: Codex 0.163.0 mcp add cannot configure it, so manually add env_vars = ["XDG_RUNTIME_DIR"] to the existing [mcp_servers.peerturn] table, preserving command/args and owner policy, and start Codex with the daemon's runtime environment. Without either discovery source, tool calls return runtime_unavailable; Codex discovery can still list tools and a later call can recover after the daemon publishes its pointer. Setup never saves the variable's value.`)
	}
	lines = append(lines,
		"Upgrades: peerturn upgrade verifies the release, reinstalls, reruns this setup and restarts the daemon; profile, peers and links are kept.",
		"Details: peerturn setup "+host+" --verbose; health: peerturn doctor.")
	if verbose {
		lines = append(lines, setupContractText(host)...)
	}
	for _, line := range lines {
		if _, err := fmt.Fprintln(stdout, line); err != nil {
			return err
		}
	}
	return nil
}
