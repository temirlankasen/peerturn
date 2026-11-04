package main

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/temirlankasen/peerturn/internal/localipc"
	"github.com/temirlankasen/peerturn/internal/store"
)

// errSetupMCPUpgrade marks setup's own default Claude entry for an older
// package; errSetupMCPPreserve marks an older Codex entry, whose approval
// policy and environment forwarding the pinned CLI cannot observe or recreate.
var (
	errSetupMCPUpgrade  = errors.New("previous PeerTurn package entry")
	errSetupMCPPreserve = errors.New("previous PeerTurn package entry with unobservable owner policy")
)

func checkSetupMCPCommand(command, root string) error {
	if !filepath.IsAbs(command) || filepath.Clean(command) != command ||
		!filepath.IsAbs(root) || filepath.Clean(root) != root {
		return invalidArguments()
	}
	command, err := store.ResolveTrustedPath(command)
	if err != nil {
		return invalidArguments()
	}
	resolved, err := store.ResolveTrustedPath(root)
	if err != nil {
		return invalidArguments()
	}
	if command == filepath.Join(resolved, "peerturn") {
		return nil
	}
	if filepath.Base(command) != "peerturn" || !previousSetupPackage(filepath.Dir(command), root) {
		return invalidArguments()
	}
	return errSetupMCPUpgrade
}

// previousSetupPackage reports whether path is an older installer version
// directory beside root. A foreign or custom package location never
// qualifies; the older directory itself may already be removed.
func previousSetupPackage(path, root string) bool {
	home, err := os.UserHomeDir()
	if err != nil || !filepath.IsAbs(path) || filepath.Clean(path) != path ||
		!filepath.IsAbs(root) || filepath.Clean(root) != root {
		return false
	}
	resolved := make([]string, 0, 3)
	for _, value := range []string{home, path, root} {
		canonical, resolveErr := store.ResolveTrustedPath(value)
		if resolveErr != nil {
			return false
		}
		resolved = append(resolved, canonical)
	}
	packages := filepath.Join(resolved[0], ".local", "share", "peerturn")
	return filepath.Dir(resolved[1]) == packages && filepath.Dir(resolved[2]) == packages &&
		olderSetupPackage(filepath.Base(resolved[1]), filepath.Base(resolved[2]))
}

func setupMCPUpgradeFailure() error {
	problem := localipc.NewError("mcp_upgrade_required", "needs_configuration")
	problem.Message = "The existing PeerTurn MCP entry points to an older installed package. codex mcp get does not show its tool approval policy, so setup does not replace it. Inspect the [mcp_servers.peerturn] table in the Codex config.toml and note any approval or env_vars settings. Then the owner must run codex mcp remove peerturn, then rerun peerturn setup codex with the same verified package options, restore those settings and reconnect MCP in the same selected session."
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
