package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/temirlankasen/peerturn/internal/localipc"
	"github.com/temirlankasen/peerturn/internal/store"
)

var errSetupMCPUpgrade = errors.New("previous PeerTurn package entry")

func checkSetupMCPCommand(command, root string) error {
	home, err := os.UserHomeDir()
	if err != nil || !filepath.IsAbs(command) || filepath.Clean(command) != command ||
		!filepath.IsAbs(root) || filepath.Clean(root) != root {
		return invalidArguments()
	}
	command, err = store.ResolveTrustedPath(command)
	if err != nil {
		return invalidArguments()
	}
	root, err = store.ResolveTrustedPath(root)
	if err != nil {
		return invalidArguments()
	}
	if command == filepath.Join(root, "peerturn") {
		return nil
	}
	home, err = store.ResolveTrustedPath(home)
	if err != nil {
		return invalidArguments()
	}
	packages := filepath.Join(home, ".local", "share", "peerturn")
	previous := filepath.Dir(command)
	// Only the installer's version directory may differ. A foreign command or
	// custom package location must keep the ordinary owner-policy conflict.
	if filepath.Base(command) != "peerturn" || filepath.Dir(previous) != packages || filepath.Dir(root) != packages ||
		!olderSetupPackage(filepath.Base(previous), filepath.Base(root)) {
		return invalidArguments()
	}
	return errSetupMCPUpgrade
}

func setupMCPUpgradeFailure(host string) error {
	step := "codex mcp remove peerturn"
	if host == "claude" {
		step = "claude mcp remove peerturn --scope user"
	}
	problem := localipc.NewError("mcp_upgrade_required", "needs_configuration")
	problem.Message = fmt.Sprintf("The existing PeerTurn MCP entry differs only by an older installed package path. Inspect %s mcp get peerturn and preserve owner tool/approval/environment policy. To replace it, the owner must run %s, then rerun peerturn setup %s with the same verified package options. Setup never removes or rewrites an existing entry. Restore Linux name-only XDG_RUNTIME_DIR forwarding if needed, then reconnect MCP in the same selected session.", host, step, host)
	return problem
}

type setupPackageVersion struct {
	core [3]uint64
	pre  []string
}

func parseSetupPackageVersion(name string) (setupPackageVersion, bool) {
	var result setupPackageVersion
	core, pre, hasPre := strings.Cut(name, "-")
	parts := strings.Split(core, ".")
	if len(name) > 64 || len(parts) != 3 {
		return result, false
	}
	for i, part := range parts {
		n, err := strconv.ParseUint(part, 10, 64)
		if err != nil || strconv.FormatUint(n, 10) != part {
			return result, false
		}
		result.core[i] = n
	}
	if hasPre {
		result.pre = strings.Split(pre, ".")
		for _, part := range result.pre {
			if part == "" || strings.Trim(part, "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz-") != "" ||
				(strings.Trim(part, "0123456789") == "" && len(part) > 1 && part[0] == '0') {
				return result, false
			}
		}
	}
	return result, true
}

func olderSetupPackage(previous, current string) bool {
	old, oldOK := parseSetupPackageVersion(previous)
	next, nextOK := parseSetupPackageVersion(current)
	if !oldOK || !nextOK {
		return false
	}
	if comparison := slices.Compare(old.core[:], next.core[:]); comparison != 0 {
		return comparison < 0
	}
	if len(old.pre) == 0 {
		return false
	}
	if len(next.pre) == 0 {
		return true
	}
	for i := range min(len(old.pre), len(next.pre)) {
		left, right := old.pre[i], next.pre[i]
		if left == right {
			continue
		}
		leftNumeric, rightNumeric := strings.Trim(left, "0123456789") == "", strings.Trim(right, "0123456789") == ""
		if leftNumeric != rightNumeric {
			return leftNumeric
		}
		if leftNumeric && len(left) != len(right) {
			return len(left) < len(right)
		}
		return left < right
	}
	return len(old.pre) < len(next.pre)
}
