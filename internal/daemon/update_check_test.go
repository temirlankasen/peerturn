package daemon

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/temirlankasen/peerturn/internal/localipc"
)

// The daily lookup runs once a day after the daemon settled, survives a
// failing source, and surfaces a newer release as an action in status.
func TestUpdateCheckRunsDailyAndWarnsInStatus(t *testing.T) {
	parallelDaemonTest(t)
	d, _ := pairedStores(t)
	calls := 0
	d.updateSource = func(context.Context) (string, error) {
		calls++
		if calls == 1 {
			return "", errors.New("offline")
		}
		return "99.0.0", nil
	}
	d.checkUpdates(t.Context())
	if calls != 0 {
		t.Fatal("first maintenance must not look up releases before the daemon settled")
	}
	d.updateChecked = unixNow() - updateCheckInterval
	d.checkUpdates(t.Context())
	d.checkUpdates(t.Context())
	if calls != 1 || d.latestVersion != "" {
		t.Fatalf("failed lookup: calls=%d latest=%q", calls, d.latestVersion)
	}
	d.updateChecked = unixNow() - updateCheckInterval
	d.checkUpdates(t.Context())
	if calls != 2 || d.latestVersion != "99.0.0" {
		t.Fatalf("newer release not recorded: calls=%d latest=%q", calls, d.latestVersion)
	}
	value, err := d.Dispatch(t.Context(), localipc.Request{Method: "status", Session: d.state.Label})
	if err != nil {
		t.Fatal(err)
	}
	status, ok := value.(Status)
	if !ok || status.LatestVersion != "99.0.0" || !strings.Contains(strings.Join(status.Warnings, "\n"), "run peerturn upgrade") {
		t.Fatalf("status does not point to the upgrade: %+v", value)
	}
}
