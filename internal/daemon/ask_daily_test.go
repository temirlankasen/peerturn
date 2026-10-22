package daemon

import (
	"testing"

	"github.com/temirlankasen/peerturn/internal/localipc"
)

func TestAskDailyQuotaAcrossPeersCleanupAndRestart(t *testing.T) {
	dir := stateDir(t)
	if _, err := Initialize(t.Context(), InitOptions{StateDir: dir, RelayURL: "https://relay.example", SessionLabel: "daily", FilePermissions: true}); err != nil {
		t.Fatal(err)
	}
	d, err := Open(t.Context(), Options{StateDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if closeErr := d.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	})
	day := unixNow() / 86400 * 86400
	for i := range 24 {
		now := day + 3600
		if i >= 12 {
			now += 3601
		}
		run, reserveErr := d.reserveAskBudget(randomID(), randomID(), randomID(), randomID(), now)
		if reserveErr != nil {
			t.Fatal(reserveErr)
		}
		// Failed or successful completion must not refund the reservation.
		if err = d.finishAskBudget(run.ID); err != nil {
			t.Fatal(err)
		}
		if i == 11 {
			if err = d.Close(); err != nil {
				t.Fatal(err)
			}
			d, err = Open(t.Context(), Options{StateDir: dir})
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, err = d.reserveAskBudget(randomID(), randomID(), randomID(), randomID(), day+8000); localipc.PublicError(err).Code != "budget_exceeded" {
		t.Fatal("daily quota reset by peer, conversation, cleanup, or restart", err)
	}
	if _, err = d.reserveAskBudget(randomID(), randomID(), randomID(), randomID(), day+86400); err != nil {
		t.Fatal("new UTC day did not replenish daily quota", err)
	}
	if _, err = d.reserveAskBudget(randomID(), randomID(), randomID(), randomID(), day+86399); localipc.PublicError(err).Code != "budget_exceeded" {
		t.Fatal("clock rollback crossed the daily boundary", err)
	}
}

func TestAskDailyMalformedCounterBlocksOnlyAsk(t *testing.T) {
	dir := stateDir(t)
	if _, err := Initialize(t.Context(), InitOptions{StateDir: dir, RelayURL: "https://relay.example", SessionLabel: "malformed-quota", FilePermissions: true}); err != nil {
		t.Fatal(err)
	}
	d, err := Open(t.Context(), Options{StateDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if closeErr := d.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	})
	now := unixNow()
	d.state.AskBudget = &askBudgetState{Policy: defaultAskPolicy(), ClockFloor: now,
		DailyUTC: now / secondsPerDay, DailyUsed: -1, Runs: map[string]askBudgetReservation{}}
	if err = d.save(); err != nil {
		t.Fatal(err)
	}
	if err = d.Close(); err != nil {
		t.Fatal(err)
	}
	d, err = Open(t.Context(), Options{StateDir: dir})
	if err != nil {
		t.Fatal("malformed ask ledger disabled owner daemon", err)
	}
	if _, err = d.Dispatch(t.Context(), localipc.Request{Method: "status"}); err != nil {
		t.Fatal("malformed ask ledger disabled owner status", err)
	}
	if _, err = d.reserveAskBudget(randomID(), randomID(), randomID(), randomID(), now); localipc.PublicError(err).Code != "budget_exceeded" {
		t.Fatal("malformed daily counter reopened ask admission", err)
	}
	if d.state.AskBudget.DailyUsed != -1 {
		t.Fatal("malformed daily counter was silently reset")
	}
}
