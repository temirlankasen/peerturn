package daemon

import peercrypto "github.com/temirlankasen/peerturn/internal/crypto"

// Completed onboarding keeps only the digest needed for local idempotency.
// Possession secrets have no use once retry publication/claim has stopped.
func (d *Daemon) pruneOnboarding() error {
	changed := false
	now := unixNow()
	for id, entry := range d.state.Issued {
		if !entry.Terminal && !entry.Sent {
			continue
		}
		if entry.Descriptor.ExpiresAt <= now {
			delete(d.state.Issued, id)
			changed = true
		} else if entry.URL != "" {
			entry.URL = ""
			entry.Descriptor.EnrollmentVoucher = ""
			changed = true
		}
	}
	for id, entry := range d.state.Joining {
		if !entry.Terminal && !entry.AckSent {
			continue
		}
		if entry.Descriptor.ExpiresAt <= now {
			delete(d.state.Joining, id)
			changed = true
		} else if entry.URL != "" {
			entry.URLDigest = peercrypto.Digest([]byte(entry.URL))
			entry.URL = ""
			entry.Descriptor.EnrollmentVoucher = ""
			changed = true
		}
	}
	if changed {
		return d.save()
	}
	return nil
}

func (d *Daemon) activeInvites() int {
	active := 0
	for _, entry := range d.state.Issued {
		if !entry.Terminal && !entry.Sent {
			active++
		}
	}
	return active
}

func (d *Daemon) activeJoins() int {
	active := 0
	for _, entry := range d.state.Joining {
		if !entry.Terminal && !entry.AckSent {
			active++
		}
	}
	return active
}
