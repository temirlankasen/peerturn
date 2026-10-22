package daemon

import (
	"testing"

	"github.com/temirlankasen/peerturn/internal/localipc"
)

func TestAskPeerDailyQuotaSurvivesHourlyCleanupAndRestart(t *testing.T) {
	a, _ := pairedStores(t)
	day := unixNow() / secondsPerDay * secondsPerDay
	for i := range 12 {
		now := day + 100 + int64(i/6)*3601
		run, err := a.reserveAskBudget("peer", "parent", randomID(), randomID(), now)
		if err != nil {
			t.Fatal(err)
		}
		if err = a.finishAskBudget(run.ID); err != nil {
			t.Fatal(err)
		}
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(t.Context(), a.options)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if closeErr := reopened.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	}()
	if _, err = reopened.reserveAskBudget("peer", "parent", randomID(), randomID(), day+7400); localipc.PublicError(err).Code != "budget_exceeded" {
		t.Fatal("one peer exhausted more than its daily share", err)
	}
	if _, err = reopened.reserveAskBudget("other", "parent", randomID(), randomID(), day+7400); err != nil {
		t.Fatal("peer quota consumed another peer's share", err)
	}
}

func TestAskRestartReleasesActiveBudgetWithoutRefundOrReplay(t *testing.T) {
	a, _ := pairedStores(t)
	now := unixNow()
	run, err := a.reserveAskBudget("peer", "parent", "conversation", "old", now)
	if err != nil {
		t.Fatal(err)
	}
	if err = a.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(t.Context(), a.options)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if closeErr := reopened.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	}()
	if reopened.state.AskBudget.Runs[run.ID].Active || reopened.state.AskBudget.DailyUsed != 1 {
		t.Fatal("recovery kept capacity busy or refunded a paid reservation")
	}
	if _, err = reopened.reserveAskBudget("peer", "parent", "conversation", "old", now); err == nil {
		t.Fatal("recovery replayed old ask")
	}
	if _, err = reopened.reserveAskBudget("peer", "parent", "conversation", "new", now); err != nil {
		t.Fatal("recovery wedged new ask", err)
	}
}

func TestAskDailyRolloverPreservesRollingHour(t *testing.T) {
	a, _ := pairedStores(t)
	day := unixNow() / secondsPerDay * secondsPerDay
	for range 5 {
		run, err := a.reserveAskBudget("peer", "parent", randomID(), randomID(), day+secondsPerDay-60)
		if err != nil {
			t.Fatal(err)
		}
		if err = a.finishAskBudget(run.ID); err != nil {
			t.Fatal(err)
		}
	}
	run, err := a.reserveAskBudget("peer", "parent", randomID(), randomID(), day+secondsPerDay+60)
	if err != nil {
		t.Fatal(err)
	}
	if err = a.finishAskBudget(run.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = a.reserveAskBudget("peer", "parent", randomID(), randomID(), day+secondsPerDay+61); localipc.PublicError(err).Code != "budget_exceeded" {
		t.Fatal("UTC rollover erased rolling-hour usage", err)
	}
}
