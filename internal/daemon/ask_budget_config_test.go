package daemon

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/temirlankasen/peerturn/internal/localipc"
)

func writeAskDailyConfig(t *testing.T, path string, count int) {
	t.Helper()
	root := askDailyConfigRoot(t, path)
	data, err := root.ReadFile(filepath.Base(path))
	if err != nil {
		t.Fatal(err)
	}
	var lines []string
	for line := range strings.SplitSeq(string(data), "\n") {
		if !strings.HasPrefix(line, "ask_per_day =") {
			lines = append(lines, line)
		}
	}
	lines = append(lines, fmt.Sprintf("ask_per_day = %d", count), "")
	if err = root.WriteFile(filepath.Base(path), []byte(strings.Join(lines, "\n")), 0o600); err != nil {
		t.Fatal(err)
	}
}

func askDailyConfigRoot(t *testing.T, path string) *os.Root {
	t.Helper()
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if closeErr := root.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	})
	return root
}

func TestAskDailyOwnerConfigurationDoesNotRefundUsage(t *testing.T) {
	parallelDaemonTest(t)
	dir := stateDir(t)
	path := filepath.Join(dir, "config.toml")
	if _, err := Initialize(t.Context(), InitOptions{StateDir: dir, ConfigPath: path, RelayURL: "https://relay.example", SessionLabel: "owner-quota", FilePermissions: true}); err != nil {
		t.Fatal(err)
	}
	writeAskDailyConfig(t, path, 2)
	d, err := Open(t.Context(), Options{StateDir: dir, ConfigPath: path})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if closeErr := d.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	})
	now := unixNow()
	for range 2 {
		run, reserveErr := d.reserveAskBudget(randomID(), randomID(), randomID(), randomID(), now)
		if reserveErr != nil {
			t.Fatal(reserveErr)
		}
		if err = d.finishAskBudget(run.ID); err != nil {
			t.Fatal(err)
		}
	}
	for _, limit := range []int{1, 3} {
		if err = d.Close(); err != nil {
			t.Fatal(err)
		}
		writeAskDailyConfig(t, path, limit)
		d, err = Open(t.Context(), Options{StateDir: dir, ConfigPath: path})
		if err != nil {
			t.Fatal(err)
		}
		_, reserveErr := d.reserveAskBudget(randomID(), randomID(), randomID(), randomID(), now)
		if limit == 1 && localipc.PublicError(reserveErr).Code != "budget_exceeded" {
			t.Fatal("lowered limit refunded prior reservations", reserveErr)
		}
		if limit == 3 && reserveErr != nil {
			t.Fatal("owner limit increase was not applied", reserveErr)
		}
	}
	if err = d.Close(); err != nil {
		t.Fatal(err)
	}
	d, err = Open(t.Context(), Options{StateDir: dir, ConfigPath: path})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = d.reserveAskBudget(randomID(), randomID(), randomID(), randomID(), now); localipc.PublicError(err).Code != "budget_exceeded" {
		t.Fatal("restart refunded an unfinished reservation", err)
	}
}

func TestAskDailyConfigBoundsAndLegacyDefault(t *testing.T) {
	parallelDaemonTest(t)
	for _, value := range []string{"", "1", "4096", "0", "-1", "4097", "true", "'24'", "1.5"} {
		t.Run("count="+value, func(t *testing.T) {
			path, state := configFixture(t)
			if err := WriteConfig(path, state, "https://relay.example", ""); err != nil {
				t.Fatal(err)
			}
			root := askDailyConfigRoot(t, path)
			data, err := root.ReadFile(filepath.Base(path))
			if err != nil {
				t.Fatal(err)
			}
			replacement := ""
			if value != "" {
				replacement = "ask_per_day = " + value + "\n"
			}
			data = []byte(strings.ReplaceAll(string(data), "ask_per_day = 24\n", replacement))
			if err = root.WriteFile(filepath.Base(path), data, 0o600); err != nil {
				t.Fatal(err)
			}
			config, err := ReadConfig(path)
			valid := value == "" || value == "1" || value == "4096"
			if !valid {
				if err == nil {
					t.Fatal("invalid owner quota accepted")
				}
				return
			}
			if err != nil || config.AskPerDay <= 0 || value == "" && config.AskPerDay != defaultAskPerDay {
				t.Fatal("missing bounded default or accepted limit", config, err)
			}
		})
	}
}

func TestAskDailyLegacyStateExhaustsLastUTCday(t *testing.T) {
	parallelDaemonTest(t)
	dir := stateDir(t)
	if _, err := Initialize(t.Context(), InitOptions{StateDir: dir, RelayURL: "https://relay.example", SessionLabel: "legacy-quota", FilePermissions: true}); err != nil {
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
	d.state.AskBudget = &askBudgetState{
		Policy:     AskPolicy{DeadlineSeconds: 120, MaxOutputBytes: 16 << 10, MaxConcurrent: 2, PerPeerPerHour: 6},
		ClockFloor: now,
		Runs:       map[string]askBudgetReservation{},
	}
	if err = d.save(); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err = d.Close(); err != nil {
			t.Fatal(err)
		}
		d, err = Open(t.Context(), Options{StateDir: dir})
		if err != nil {
			t.Fatal(err)
		}
		if _, err = d.reserveAskBudget(randomID(), randomID(), randomID(), randomID(), now); localipc.PublicError(err).Code != "budget_exceeded" {
			t.Fatal("legacy hourly cleanup refunded unknown daily usage", err)
		}
	}
	if _, err = d.reserveAskBudget(randomID(), randomID(), randomID(), randomID(), (now/secondsPerDay+1)*secondsPerDay); err != nil {
		t.Fatal("legacy state remained unavailable after the next UTC boundary", err)
	}
}
