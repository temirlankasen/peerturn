package daemon

import (
	"context"
	"os"

	"github.com/temirlankasen/peerturn/internal/update"
	"github.com/temirlankasen/peerturn/internal/version"
)

// One anonymous release lookup a day tells the owner, in status and doctor,
// that a newer PeerTurn exists; peerturn upgrade does the rest. The lookup
// sends nothing about the profile and can be switched off with
// PEERTURN_NO_UPDATE_CHECK=1 for machines without GitHub access.
const (
	updateCheckInterval int64 = 24 * 60 * 60
	updateCheckDelay    int64 = 5 * 60
	updateAPI                 = "https://api.github.com"
)

func defaultUpdateSource(ctx context.Context) (string, error) {
	release, err := update.Latest(ctx, update.NewClient(), updateAPI)
	return release.Version, err
}

// checkUpdates is called from maintenance; a failure only postpones the
// next attempt, because an unreachable GitHub must never disturb custody.
func (d *Daemon) checkUpdates(ctx context.Context) {
	now := unixNow()
	// A development build has no release to compare with; tests inject a source.
	if os.Getenv("PEERTURN_NO_UPDATE_CHECK") == "1" || (d.updateSource == nil && version.Version == "dev") {
		return
	}
	if d.updateChecked == 0 {
		// Let the daemon settle before its first lookup.
		d.updateChecked = now - updateCheckInterval + updateCheckDelay
		return
	}
	if now-d.updateChecked < updateCheckInterval {
		return
	}
	d.updateChecked = now
	source := d.updateSource
	if source == nil {
		source = defaultUpdateSource
	}
	latest, err := networkCall(ctx, d, func() (string, error) { return source(ctx) })
	if err != nil {
		d.logger().Info("update check failed", "code", safeCode(err))
		return
	}
	// A build without a release version cannot be older than anything; the
	// injected source in tests still records what it was told.
	if !update.ValidVersion(version.Version) || update.Newer(version.Version, latest) {
		d.latestVersion = latest
		d.logger().Info("update available", "latest", latest)
	}
}
