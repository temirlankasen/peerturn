package daemon

import (
	"testing"

	"github.com/temirlankasen/peerturn/internal/localipc"
)

func TestAskBudgetPersistsReservationsAndClockFloor(t *testing.T) {
	dir := stateDir(t)
	if _, err := Initialize(t.Context(), InitOptions{StateDir: dir, RelayURL: "https://relay.example", SessionLabel: "budget", FilePermissions: true}); err != nil {
		t.Fatal(err)
	}
	d, err := Open(t.Context(), Options{StateDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	now := unixNow()
	run, err := d.reserveAskBudget("peer", "parent", "conversation", "message", now)
	if err != nil || run.Deadline != now+120 {
		t.Fatal("missing receiver deadline", run, err)
	}
	if _, err = d.reserveAskBudget("other-peer", "parent", "different", "different", now); localipc.PublicError(err).Code != "responder_busy" {
		t.Fatal("overlapping parent admitted", err)
	}
	if err = d.Close(); err != nil {
		t.Fatal(err)
	}
	d, err = Open(t.Context(), Options{StateDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if closeErr := d.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	})
	if _, err = d.reserveAskBudget("peer", "parent", "conversation", "message", now); localipc.PublicError(err).Code != "in_doubt" {
		t.Fatal("restarted reservation retried", err)
	}
	if err = d.finishAskBudget(run.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = d.reserveAskBudget("peer", "parent", "next", "next", now-1); localipc.PublicError(err).Code != "budget_exceeded" {
		t.Fatal("clock rollback resets quota", err)
	}
}

func TestAskBudgetHourlyPeerQuotaDoesNotResetByConversation(t *testing.T) {
	dir := stateDir(t)
	if _, err := Initialize(t.Context(), InitOptions{StateDir: dir, RelayURL: "https://relay.example", SessionLabel: "quota", FilePermissions: true}); err != nil {
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
	for range 6 {
		run, reserveErr := d.reserveAskBudget("peer", "parent", randomID(), randomID(), now)
		if reserveErr != nil {
			t.Fatal(reserveErr)
		}
		if err = d.finishAskBudget(run.ID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = d.reserveAskBudget("peer", "parent", randomID(), randomID(), now); localipc.PublicError(err).Code != "budget_exceeded" {
		t.Fatal("sender reset hourly limit", err)
	}
	if _, err = d.reserveAskBudget("other-peer", "parent", randomID(), randomID(), now); err != nil {
		t.Fatal("unrelated peer was charged", err)
	}
}
