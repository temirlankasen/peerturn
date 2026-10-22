package daemon

import (
	"errors"
	"os"

	"github.com/temirlankasen/peerturn/internal/fault"
)

// configureAskBudget reads only the owner's local config. Updating a limit never
// resets the sealed counter, reservations, or clock floor.
func (d *Daemon) configureAskBudget() error {
	perDay := 0
	if d.options.ConfigPath != "" {
		config, err := ReadConfig(d.options.ConfigPath)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err == nil {
			if config.StateDir != d.options.StateDir {
				return fault.New(fault.InvalidEncoding)
			}
			perDay = config.AskPerDay
		}
	}
	b := d.state.AskBudget
	if b == nil {
		if perDay == 0 || perDay == defaultAskPerDay {
			return nil
		}
		b = &askBudgetState{Policy: defaultAskPolicy(), Runs: map[string]askBudgetReservation{}}
	}
	next := *b
	changed := upgradeAskBudget(&next)
	if perDay != 0 && next.Policy.PerDay != perDay {
		next.Policy.PerDay = perDay
		changed = true
	}
	if !validAskBudget(&next) {
		// A broken ask ledger must close ask admission without disabling the
		// owner's daemon controls or unrelated coauthor delivery.
		return nil
	}
	if changed || d.state.AskBudget == nil {
		d.state.AskBudget = &next
		return d.save()
	}
	return nil
}
