package main

import (
	"context"
	"path/filepath"

	"github.com/temirlankasen/peerturn/internal/daemon"
)

// claudePluginConfig configures the verified helper and, when this profile is
// already bound, names that session so the plugin restores its helper after
// an upgrade or reboot without a manual peerturn command. Other sessions stay
// untouched: the plugin starts nothing for a session the hint does not name.
func claudePluginConfig(ctx context.Context, root string) []string {
	args := []string{"--config", "peerturn_binary=" + filepath.Join(root, "peerturn")}
	dir, err := daemon.DefaultStateDir()
	if err != nil {
		return args
	}
	if session := daemon.BoundClaudeSession(ctx, dir); session != "" {
		args = append(args, "--config", "peerturn_bound_session="+session)
	}
	return args
}
