package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

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
	absolute, err := filepath.Abs(*root)
	if err != nil || verifySetupTree(absolute, *pin) != nil || verifyMarketplace(absolute, args[0]) != nil {
		return writeFailure(stdout, stderr, invalidArguments())
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	invoke := func(ctx context.Context, name string, argv []string, output io.Writer) error {
		binary, lookupErr := exec.LookPath(name)
		if lookupErr != nil {
			return lookupErr
		}
		binary, lookupErr = filepath.Abs(binary)
		if lookupErr != nil {
			return lookupErr
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
		command.Path = binary
		command.Args = append([]string{binary}, argv...)
		command.Stdout, command.Stderr, command.Stdin = output, stderr, os.Stdin
		return command.Run()
	}
	if err = installSetup(ctx, args[0], absolute, stdout, invoke); err != nil {
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
	if err := invoke(ctx, host, []string{"plugin", "marketplace", "add", root}, stdout); err != nil {
		return err
	}
	if err := observeSetupMarketplace(ctx, host, root, invoke); err != nil {
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
	if err := invoke(ctx, host, installArgs, stdout); err != nil {
		return err
	}
	if host == "claude" {
		if err := invoke(ctx, host, []string{"plugin", "update", "peerturn@peerturn"}, stdout); err != nil {
			return err
		}
	}
	if err := observeSetupPlugin(ctx, host, root, invoke); err != nil {
		return err
	}
	_, err := fmt.Fprintln(stdout, "Host-reported marketplace source and observable plugin files matched the installer inventory. This is a local integrity match, not independent publisher authentication or proof of activation/current-session binding. Review and accept the host's actual hook trust prompt; never bypass it.")
	if err != nil {
		return err
	}
	if host == "claude" {
		_, err = fmt.Fprintln(stdout, "peerturn_binary is configured to the inventory-matched package executable. Run /reload-plugins in the selected session; if necessary resume the same session. Then peerturn session bind --current from its tool child. Do not create a replacement session or change its permission mode/model.")
	} else {
		_, err = fmt.Fprintln(stdout, "If the running session has not activated the plugin, restart/resume that same session and accept hook trust. Initialize with peerturn init --codex-current and trusted relay options from its tool child. Shared daemon only; embedded Codex is unsupported. Never change the owner's permission mode/model.")
	}
	return err
}
