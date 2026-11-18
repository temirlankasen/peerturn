package daemon

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"

	"github.com/temirlankasen/peerturn/internal/store"
)

var claudeSessionID = regexp.MustCompile(`^[a-f0-9]{8}(?:-[a-f0-9]{4}){3}-[a-f0-9]{12}$`)

// BoundClaudeSession names the Claude session this profile is pinned to, so
// setup can tell the plugin which session brings its helper back after an
// upgrade or reboot. It is only a hint for launching the helper: the daemon
// still checks the pin and a fresh native proof. Any doubt returns "" and
// leaves the plugin as it was. A key in the OS keystore is never opened from
// setup, so an isolated profile is not asked for keychain access here.
func BoundClaudeSession(ctx context.Context, dir string) string {
	if _, err := readWrappingMetadata(dir); !errors.Is(err, os.ErrNotExist) {
		return ""
	}
	if ctx.Err() != nil {
		return ""
	}
	sealer, err := store.OpenFileSealer(filepath.Join(dir, "seal.key"))
	if err != nil {
		return ""
	}
	state, err := loadState(dir, sealer)
	if err != nil || state.Codex != nil || !claudeSessionID.MatchString(state.NativeSessionID) {
		return ""
	}
	return state.NativeSessionID
}
