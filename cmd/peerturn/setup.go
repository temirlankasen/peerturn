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
	if err = installSetup(ctx, args[0], absolute, stdout, invoke); err != nil {
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

func installSetup(ctx context.Context, host, root string, stdout io.Writer, invoke setupInvoke) error {
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
	if err = inspectSetupMarketplace(ctx, host, root, invoke, true); err != nil {
		return err
	}
	if err = invoke(ctx, host, []string{"plugin", "marketplace", "add", root}, stdout); err != nil {
		return err
	}
	if err = observeSetupMarketplace(ctx, host, root, invoke); err != nil {
		return err
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
	if err = observeSetupPlugin(ctx, host, root, invoke); err != nil {
		return err
	}
	if err = registerSetupMCP(ctx, host, root, invoke); err != nil {
		return err
	}
	if _, err = fmt.Fprintf(stdout, "Runtime: %s %s; verification_status=%s; minimum=%s. Available capabilities: setup. Live binding/coauthor/owner-boundary contracts: not_observed; inspect status --current --json / doctor after activation.\n", runtime.Runtime, runtime.Version, runtime.Status, runtime.Minimum); err != nil {
		return err
	}
	_, err = fmt.Fprintln(stdout, "Host-reported marketplace source and observable plugin files matched the installer inventory. This is a local integrity match, not independent publisher authentication or proof of activation/current-session binding. Review and accept the host's actual hook trust prompt; never bypass it.")
	if err != nil {
		return err
	}
	if host == "claude" {
		_, err = fmt.Fprintln(stdout, "peerturn_binary is configured to the inventory-matched package executable. Run /reload-plugins in the selected session; if necessary resume the same session. Then peerturn session bind --current from its tool child. Do not create a replacement session or change its permission mode/model.")
	} else {
		_, err = fmt.Fprintln(stdout, "If the running session has not activated the plugin, restart/resume that same session and accept hook trust. Initialize with peerturn init --codex-current and trusted relay options from its tool child. Shared daemon only; embedded Codex is unsupported. Never change the owner's permission mode/model.")
		if err == nil {
			_, err = fmt.Fprintln(stdout, `On Linux, MCP can discover the daemon through its private-state runtime pointer. Name-only forwarding is an alternative: Codex 0.163.0 mcp add cannot configure it, so manually add env_vars = ["XDG_RUNTIME_DIR"] to the existing [mcp_servers.peerturn] table, preserving command/args and owner policy, and start Codex with the daemon's runtime environment. Without either discovery source, tool calls return runtime_unavailable; Codex discovery can still list tools and a later call can recover after the daemon publishes its pointer. Setup never saves the variable's value.`)
		}
	}
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(stdout, "MCP registration checked; this is not proof of activation or binding. Keep the daemon running in the same private runtime environment. In that same session, open /mcp and reconnect peerturn after native binding is ready (reload/resume that same session if needed). Confirm peer_join is listed, then call peer_status with {}: binding must be native_claude (Claude) or codex_shared_daemon (Codex), with capabilities.attach_existing=true. Codex host MCP binds on its first tool call using host metadata, not CODEX_THREAD_ID at startup; tool listing alone supplies no authority. Compare its session with peerturn status --current --json from that session's tool child. If unavailable, report the typed failure. The owner can run peerturn join --current --link-stdin in that session's tool child, supplying stdin or an owner-prepared private file; never embed an invitation in a tool command or configure a test label, native UUID or token as a fallback. A fresh live host rerun remains required.")
	return err
}
