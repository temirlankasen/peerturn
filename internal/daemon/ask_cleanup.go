package daemon

import (
	"context"
	"encoding/json"
	"os"
	"runtime"

	"github.com/temirlankasen/peerturn/internal/claude"
)

// Recovery never races a still-running orphan or touches an inferred owner
// session. A failed proof closes asks only; coauthor and owner controls continue.
func (d *Daemon) recoverAskArtifacts(ctx context.Context) error {
	forks, err := d.db.ListAskForks(ctx)
	if err != nil || len(forks) == 0 {
		return err
	}
	if runtime.GOOS != "linux" {
		return &claude.ResponderError{Code: claude.ForkCleanupFailed}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	for _, fork := range forks {
		var snapshot struct{ Session, ConfigurationDirectory string }
		if json.Unmarshal(fork.Snapshot, &snapshot) != nil || snapshot.Session == "" || snapshot.ConfigurationDirectory == "" {
			return &claude.ResponderError{Code: claude.ForkCleanupFailed}
		}
		child := string(fork.NativeID)
		active, activeErr := claude.ResponderForkActive(child)
		if activeErr != nil || active {
			return &claude.ResponderError{Code: claude.ForkCleanupFailed}
		}
		options := claude.ResponderOptions{HomeDirectory: home}
		options.RuntimeEnv = []string{"CLAUDE_CONFIG_DIR=" + snapshot.ConfigurationDirectory}
		if err = claude.CleanupResponderArtifacts(options, snapshot.Session, child); err != nil {
			return err
		}
		if err = d.db.ForgetAskFork(ctx, fork.RunID, fork.NativeID); err != nil {
			return err
		}
	}
	return nil
}
