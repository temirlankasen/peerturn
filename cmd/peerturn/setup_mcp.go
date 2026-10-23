package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"slices"

	"github.com/temirlankasen/peerturn/internal/codec"
	"github.com/temirlankasen/peerturn/internal/localipc"
)

func registerSetupMCP(ctx context.Context, host, root string, invoke setupInvoke) error {
	var present bool
	var err error
	if host == "codex" {
		present, err = observeSetupCodexMCP(ctx, root, invoke)
	} else {
		present, err = observeSetupClaudeMCP(root)
	}
	upgrade := errors.Is(err, errSetupMCPUpgrade)
	if (err != nil && !upgrade) || ctx.Err() != nil {
		return setupMCPFailure(host)
	}
	if host == "claude" {
		var output setupOutput
		queryErr := invoke(ctx, host, []string{"mcp", "get", "peerturn"}, &output)
		var exited interface{ ExitCode() int }
		absent := errors.As(queryErr, &exited) && exited.ExitCode() == 1
		// Native JSON establishes the candidate configuration. The CLI exit
		// corroborates visibility without interpreting its diagnostic wording.
		if output.overflow || ctx.Err() != nil || (present && queryErr != nil) || (!present && !absent) {
			return setupMCPFailure(host)
		}
	}
	if upgrade {
		return setupMCPUpgradeFailure(host)
	}
	if present {
		return nil
	}
	args := []string{"mcp", "add"}
	if host == "claude" {
		args = append(args, "--transport", "stdio", "--scope", "user")
	}
	// Configuration selects the executable only. Authority comes from the
	// existing native proof in --current, never a saved UUID or credential.
	args = append(args, "peerturn", "--", filepath.Join(root, "peerturn"), "mcp", "--current")
	if err := invoke(ctx, host, args, io.Discard); err != nil {
		return setupMCPFailure(host)
	}
	return nil
}

func observeSetupCodexMCP(ctx context.Context, root string, invoke setupInvoke) (bool, error) {
	var output setupOutput
	if err := invoke(ctx, "codex", []string{"mcp", "list", "--json"}, &output); err != nil || output.overflow {
		return false, setupMCPFailure("codex")
	}
	var listing []map[string]json.RawMessage
	if codec.Decode(output.Bytes(), 64*1024, &listing) != nil || listing == nil {
		return false, setupMCPFailure("codex")
	}
	found := false
	seen := make(map[string]bool)
	for _, entry := range listing {
		var name string
		if codec.Decode(entry["name"], 64*1024, &name) != nil || name == "" || seen[name] {
			return false, setupMCPFailure("codex")
		}
		seen[name] = true
		found = found || name == "peerturn"
	}
	if !found {
		return false, nil
	}
	// list omits tool policy in this pinned version; get includes it.
	output = setupOutput{}
	if err := invoke(ctx, "codex", []string{"mcp", "get", "peerturn", "--json"}, &output); err != nil || output.overflow {
		return false, setupMCPFailure("codex")
	}
	return true, checkSetupMCP(output.Bytes(), root)
}

func checkSetupMCP(data []byte, root string) error {
	var entry struct {
		Name           string   `json:"name"`
		Enabled        bool     `json:"enabled"`
		DisabledReason *string  `json:"disabled_reason"`
		EnabledTools   []string `json:"enabled_tools"`
		DisabledTools  []string `json:"disabled_tools"`
		StartupTimeout *float64 `json:"startup_timeout_sec"`
		ToolTimeout    *float64 `json:"tool_timeout_sec"`
		Transport      struct {
			Type    string            `json:"type"`
			Command string            `json:"command"`
			Args    []string          `json:"args"`
			Env     map[string]string `json:"env"`
			EnvVars []string          `json:"env_vars"`
			Cwd     *string           `json:"cwd"`
		} `json:"transport"`
	}
	if codec.Decode(data, 64*1024, &entry) != nil {
		return invalidArguments()
	}
	if entry.Name != "peerturn" || !entry.Enabled || entry.DisabledReason != nil ||
		entry.EnabledTools != nil || entry.DisabledTools != nil || entry.StartupTimeout != nil || entry.ToolTimeout != nil ||
		entry.Transport.Type != "stdio" || !slices.Equal(entry.Transport.Args, []string{"mcp", "--current"}) ||
		entry.Transport.Env != nil || entry.Transport.Cwd != nil ||
		(len(entry.Transport.EnvVars) != 0 && !slices.Equal(entry.Transport.EnvVars, []string{"XDG_RUNTIME_DIR"})) {
		return invalidArguments()
	}
	return checkSetupMCPCommand(entry.Transport.Command, root)
}

func setupMCPFailure(host string) error {
	problem := localipc.NewError("mcp_registration_failed", "needs_configuration")
	problem.Message = fmt.Sprintf("Inspect %s mcp get peerturn and resolve any existing conflicting entry, preserving owner tool/approval/environment policy, then rerun peerturn setup %s. The server must use the installed absolute peerturn executable with arguments mcp --current; do not save native UUIDs or tokens.", host, host)
	return problem
}
