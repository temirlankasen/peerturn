package daemon

import (
	"context"
	"net/url"
	"slices"
	"time"

	"github.com/temirlankasen/peerturn/internal/invite"
	"github.com/temirlankasen/peerturn/internal/localipc"
)

func (d *Daemon) addOwnerInvite(hash, epoch string, link invite.Link, now time.Time) {
	u, err := url.Parse(link.FetchURL())
	if err != nil {
		return
	}
	r := ownerInvite{epoch: epoch, created: now, expires: now.Add(ownerInviteTTL), handle: randomID()[:12], host: u.Host, link: []byte(link.SecretURL())}
	// The public routing prefix distinguishes handles without exposing fragments.
	r.hint = u.Path[:min(len(u.Path), len("/i/")+6)]
	d.joinOrigins.ownerOrder++
	r.order = d.joinOrigins.ownerOrder
	// Capture only the lookup key. The timer must not retain a second capability.
	r.timer = time.AfterFunc(ownerInviteTTL, func() {
		d.mu.Lock()
		defer d.mu.Unlock()
		d.expireOwnerInvites(time.Now())
	})
	d.joinOrigins.owner[hash] = r
	d.joinOrigins.expiredEpoch = ""
}

func (d *Daemon) removeOwnerInvite(hash string) {
	r := d.joinOrigins.owner[hash]
	if r.timer != nil {
		r.timer.Stop()
	}
	clear(r.link)
	delete(d.joinOrigins.owner, hash)
}

func (d *Daemon) consumeOwnerInvite(hash, handle string) {
	// A fresh owner repaste during released-mu I/O belongs to its own attempt.
	if current, exists := d.joinOrigins.owner[hash]; exists && current.handle == handle {
		d.removeOwnerInvite(hash)
	}
}

func (d *Daemon) wipeOwnerInvites() {
	for hash := range d.joinOrigins.owner {
		d.removeOwnerInvite(hash)
	}
	d.joinOrigins.expiredEpoch = ""
}

func (d *Daemon) expireOwnerInvites(now time.Time) {
	for hash, r := range d.joinOrigins.owner {
		if !now.Before(r.expires) {
			d.joinOrigins.expiredEpoch = r.epoch
			d.removeOwnerInvite(hash)
		}
	}
}

func (d *Daemon) missingOwnerInvite(epoch string) error {
	code := "no_owner_invitation"
	if epoch != "" && d.joinOrigins.expiredEpoch == epoch {
		code = "owner_invitation_expired"
	}
	return localipc.NewError(code, "rejected")
}

func (d *Daemon) pendingOwnerInvites(epoch string) []localipc.OwnerInvitation {
	d.expireOwnerInvites(time.Now())
	var result []localipc.OwnerInvitation
	for _, r := range d.joinOrigins.owner {
		if r.epoch == epoch {
			result = append(result, localipc.OwnerInvitation{Handle: r.handle, RelayHost: r.host, RoutingHint: r.hint, AgeSeconds: int64(time.Since(r.created) / time.Second)})
		}
	}
	slices.SortFunc(result, func(a, b localipc.OwnerInvitation) int {
		if a.Handle < b.Handle {
			return -1
		}
		if a.Handle > b.Handle {
			return 1
		}
		return 0
	})
	return result
}

func (d *Daemon) joinOwnerInvitation(ctx context.Context, r localipc.Request) (any, error) {
	if r.Capability == "" {
		return nil, localipc.NewError("unbound", "unbound")
	}
	if err := d.checkJoinCapability(r.Capability); err != nil {
		return nil, err
	}
	epoch := d.capabilities[r.Capability].nativeEpoch
	choices := d.pendingOwnerInvites(epoch)
	if len(choices) == 0 {
		return nil, d.missingOwnerInvite(epoch)
	}
	if r.InvitationHandle == "" && len(choices) > 1 {
		err := localipc.NewError("ambiguous_owner_invitation", "rejected")
		err.Invitations = choices
		return nil, err
	}
	for hash, pending := range d.joinOrigins.owner {
		if pending.epoch == epoch && (r.InvitationHandle == "" || pending.handle == r.InvitationHandle) {
			// Consume even on a failed attempt; uncertain outcomes must not cause
			// automatic resubmission. Rechecks during I/O still need owner proof.
			defer d.consumeOwnerInvite(hash, pending.handle)
			return d.joinWithCapability(ctx, string(pending.link), r.Capability)
		}
	}
	return nil, localipc.NewError("no_owner_invitation", "rejected")
}
