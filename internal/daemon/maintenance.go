package daemon

import (
	"context"
)

// maintain renews a lapsed certificate, expires custody and tells peers which
// own sequences were lost. A never-relayed or unreceived expired packet used
// to fence the channel until a new invitation; the announced floor replaces it.
func (d *Daemon) maintain(ctx context.Context) error {
	if err := d.renewIdentity(ctx, false); err != nil {
		return err
	}
	if err := d.pruneOnboarding(); err != nil {
		return err
	}
	now := unixNow()
	expired, err := d.db.ExpiredOutboxGrants(ctx, now)
	if err != nil {
		return err
	}
	if len(expired) > 0 || now-d.lastMaintenance >= 60 {
		// Announce before expiry collects old outbox rows: after a long stop
		// the lost packet's row is the only evidence of the loss.
		if err = d.announceLoss(ctx); err != nil {
			return err
		}
		if err = d.db.Expire(ctx, now); err != nil {
			return err
		}
		d.lastMaintenance = now
	}
	return nil
}
