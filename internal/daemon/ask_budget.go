package daemon

import (
	"context"

	"github.com/temirlankasen/peerturn/internal/fault"
	"github.com/temirlankasen/peerturn/internal/localipc"
	"github.com/temirlankasen/peerturn/internal/protocol"
	"github.com/temirlankasen/peerturn/internal/store"
)

const (
	defaultAskPerDay     = 24
	defaultAskPeerPerDay = 12
	maxAskPerDay         = 4096
	secondsPerDay        = 86400
)

// AskPolicy is receiver-owned and sealed with its reservations. Peer-selected
// conversation and budget identifiers never reset a peer's hourly usage.
type AskPolicy struct {
	DeadlineSeconds int64 `json:"deadline_seconds"`
	MaxOutputBytes  int   `json:"max_output_bytes"`
	MaxConcurrent   int   `json:"max_concurrent"`
	PerPeerPerHour  int   `json:"per_peer_per_hour"`
	PerDay          int   `json:"per_day"`
	PerPeerPerDay   int   `json:"per_peer_per_day"`
}

type askBudgetState struct {
	Policy     AskPolicy                       `json:"policy"`
	ClockFloor int64                           `json:"clock_floor"`
	DailyUTC   int64                           `json:"daily_utc"`
	DailyUsed  int                             `json:"daily_used"`
	Runs       map[string]askBudgetReservation `json:"runs"`
}

type askBudgetReservation struct {
	ID           string `json:"id"`
	Peer         string `json:"peer"`
	Parent       string `json:"parent"`
	Conversation string `json:"conversation"`
	Message      string `json:"message"`
	Started      int64  `json:"started"`
	Deadline     int64  `json:"deadline"`
	Active       bool   `json:"active"`
}

func defaultAskPolicy() AskPolicy {
	return AskPolicy{DeadlineSeconds: 120, MaxOutputBytes: 16 << 10, MaxConcurrent: 2, PerPeerPerHour: 6, PerDay: defaultAskPerDay, PerPeerPerDay: defaultAskPeerPerDay}
}

func validAskPolicy(p AskPolicy) bool {
	return p.DeadlineSeconds > 0 && p.DeadlineSeconds <= 600 &&
		p.MaxOutputBytes > 0 && p.MaxOutputBytes <= 16<<10 &&
		p.MaxConcurrent > 0 && p.MaxConcurrent <= 2 &&
		p.PerPeerPerHour > 0 && p.PerPeerPerHour <= 60 &&
		p.PerDay > 0 && p.PerDay <= maxAskPerDay && p.PerPeerPerDay > 0 && p.PerPeerPerDay <= maxAskPerDay
}

// Legacy state retained only one hour of completed reservations, so its actual
// daily usage is unknowable. Exhaust that UTC day instead of refunding usage.
func upgradeAskBudget(b *askBudgetState) bool {
	changed := false
	if b.Policy.PerPeerPerDay == 0 {
		b.Policy.PerPeerPerDay = defaultAskPeerPerDay
		changed = true
	}
	if b.Policy.PerDay != 0 || b.DailyUTC != 0 || b.DailyUsed != 0 {
		return changed
	}
	b.Policy.PerDay = defaultAskPerDay
	if b.ClockFloor > 0 {
		b.DailyUTC, b.DailyUsed = b.ClockFloor/secondsPerDay, defaultAskPerDay
	}
	return true
}

func validAskBudget(b *askBudgetState) bool {
	return validAskPolicy(b.Policy) && b.Runs != nil && b.ClockFloor >= 0 &&
		b.DailyUsed >= 0 && b.DailyUsed <= maxAskPerDay &&
		(b.ClockFloor == 0 && b.DailyUTC == 0 && b.DailyUsed == 0 ||
			b.ClockFloor > 0 && b.DailyUTC == b.ClockFloor/secondsPerDay && b.DailyUsed > 0)
}

// reserveAskBudget must run after all preflights, under the daemon operation
// mutex. A saved reservation is never retried after restart, even if no native
// result was observed. Failed persistence closes further admissions.
func (d *Daemon) reserveAskBudget(peer, parent, conversation, message string, now int64) (askBudgetReservation, error) {
	var empty askBudgetReservation
	if err := d.checkAskBudget(peer, parent, conversation, message, now); err != nil {
		return empty, err
	}
	b := d.state.AskBudget
	if b == nil {
		b = &askBudgetState{Policy: defaultAskPolicy(), Runs: map[string]askBudgetReservation{}}
		d.state.AskBudget = b
	}
	upgradeAskBudget(b)
	day, dailyUsed := now/secondsPerDay, b.DailyUsed
	if day != b.DailyUTC {
		dailyUsed = 0
	}
	for id, run := range b.Runs {
		if !run.Active && run.Started/secondsPerDay < day && now-run.Started >= 3600 {
			delete(b.Runs, id)
		}
	}
	run := askBudgetReservation{ID: randomID(), Peer: peer, Parent: parent, Conversation: conversation,
		Message: message, Started: now, Deadline: now + b.Policy.DeadlineSeconds, Active: true}
	b.Runs[run.ID], b.ClockFloor = run, now
	b.DailyUTC, b.DailyUsed = day, dailyUsed+1
	if err := d.save(); err != nil {
		return empty, err
	}
	return run, nil
}

// checkAskBudget is a read-only, cheap admission check before OS services,
// executable qualification or transcript parsing. Reservation rechecks it.
func (d *Daemon) checkAskBudget(peer, parent, conversation, message string, now int64) error {
	if d.persistenceFailed {
		return localipc.NewError("store_unavailable", "store_unavailable")
	}
	if peer == "" || parent == "" || conversation == "" || message == "" || now <= 0 {
		return localipc.NewError("invalid_arguments", "rejected")
	}
	b := d.state.AskBudget
	if b == nil {
		b = &askBudgetState{Policy: defaultAskPolicy(), Runs: map[string]askBudgetReservation{}}
	}
	next := *b
	upgradeAskBudget(&next)
	b = &next
	if !validAskBudget(b) || now < b.ClockFloor {
		return localipc.NewError("budget_exceeded", "budget_exceeded")
	}
	day, dailyUsed := now/secondsPerDay, b.DailyUsed
	if day != b.DailyUTC {
		dailyUsed = 0
	}
	active, used, peerDaily, knownDaily, retained := 0, 0, 0, 0, 0
	for _, run := range b.Runs {
		if run.Message == message {
			return localipc.NewError("in_doubt", "in_doubt")
		}
		if run.Active {
			active++
			if run.Parent == parent || run.Conversation == conversation {
				return localipc.NewError("responder_busy", "responder_busy")
			}
		}
		if run.Peer == peer && now-run.Started < 3600 {
			used++
		}
		if run.Started/secondsPerDay == day {
			knownDaily++
			if run.Peer == peer {
				peerDaily++
			}
		}
		if run.Active || run.Started/secondsPerDay >= day || now-run.Started < 3600 {
			retained++
		}
	}
	if active >= b.Policy.MaxConcurrent {
		return localipc.NewError("responder_busy", "responder_busy")
	}
	// Older ledgers discarded completed hourly rows. Attribute unknown daily
	// usage conservatively to each peer until the next UTC day.
	peerDaily += max(0, dailyUsed-knownDaily)
	if used >= b.Policy.PerPeerPerHour || dailyUsed >= b.Policy.PerDay || peerDaily >= b.Policy.PerPeerPerDay {
		return localipc.NewError("budget_exceeded", "budget_exceeded")
	}
	if retained >= 4096 {
		return localipc.NewError("budget_exceeded", "budget_exceeded")
	}
	return nil
}

func (d *Daemon) recoverAskBudget(ctx context.Context) error {
	if d.state.AskBudget == nil || !validAskBudget(d.state.AskBudget) {
		return nil
	}
	changed := false
	for id, run := range d.state.AskBudget.Runs {
		if run.Active {
			if protocol.ValidID(run.Message) {
				item, err := d.db.Inbox(ctx, run.Message)
				if err != nil && !fault.IsCode(err, store.NotFound) {
					return err
				}
				// A crash during out-of-lock preflight has a paid reservation
				// but no responder row yet. Close its pending intent as well.
				if err == nil && (item.State == "pending" || item.State == "intent") {
					if _, err = d.db.RejectPendingDelivery(ctx, run.Message, "runtime_unavailable", unixNow()); err != nil {
						return err
					}
				}
			}
			run.Active = false
			d.state.AskBudget.Runs[id] = run
			changed = true
		}
	}
	if changed {
		return d.save()
	}
	return nil
}

func (d *Daemon) finishAskBudget(id string) error {
	if d.state.AskBudget == nil {
		return localipc.NewError("invalid_state", "in_doubt")
	}
	run, found := d.state.AskBudget.Runs[id]
	if !found {
		return localipc.NewError("invalid_state", "in_doubt")
	}
	run.Active = false
	d.state.AskBudget.Runs[id] = run
	return d.save()
}
