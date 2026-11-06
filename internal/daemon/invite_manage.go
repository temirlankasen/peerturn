package daemon

import (
	"context"
	"sort"
	"time"

	"github.com/temirlankasen/peerturn/internal/fault"
	"github.com/temirlankasen/peerturn/internal/invite"
	"github.com/temirlankasen/peerturn/internal/localipc"
	"github.com/temirlankasen/peerturn/internal/store"
)

// legacyInviteLifetime is what relays before 0.2.0 accept at most.
const legacyInviteLifetime int64 = 600

// IssuedInvitation is one unused or claimed link this session created.
type IssuedInvitation struct {
	ID        string `json:"id"`
	NotBefore int64  `json:"not_before"`
	ExpiresAt int64  `json:"expires_at"`
	State     string `json:"state"`
}

// inviteLifetime bounds a requested lifetime: at least a minute to be
// usable, at most the profile's maximum.
func inviteLifetime(requested int64) (int64, error) {
	if requested == 0 {
		return invite.MaxLifetime, nil
	}
	if requested < 60 || requested > invite.MaxLifetime {
		return 0, localipc.Explain("invalid_arguments", "rejected", "An invitation lives between 1 minute and 24 hours; pass --lifetime like 10m or 24h.")
	}
	return requested, nil
}

// reusableInvite is the newest unused link with at least an hour left that
// does not outlive an explicitly requested lifetime (a peer before 0.2.0
// needs a ten-minute link, not the day-long one from earlier). It answers
// --reuse: the owner who lost a link gets it again without spending the
// relay's active-invitation quota.
func (d *Daemon) reusableInvite(now, requested int64) *issuedInvite {
	var best *issuedInvite
	for _, entry := range d.state.Issued {
		lifetime := entry.Descriptor.ExpiresAt - entry.Descriptor.NotBefore
		// A link is worth printing again while an hour, or half of a shorter
		// lifetime, remains; a nearly dead link would only fail the recipient.
		if entry.Terminal || entry.Sent || entry.Claim != nil || entry.URL == "" || entry.Descriptor.ExpiresAt-now < min(60*60, lifetime/2) {
			continue
		}
		if requested > 0 && lifetime > requested {
			continue
		}
		// Links minted in the same second tie on time; the ID breaks the tie so
		// repeated --reuse calls print the same link.
		if best == nil || entry.Descriptor.ExpiresAt > best.Descriptor.ExpiresAt || (entry.Descriptor.ExpiresAt == best.Descriptor.ExpiresAt && entry.Descriptor.InviteID > best.Descriptor.InviteID) {
			best = entry
		}
	}
	return best
}

func (d *Daemon) invitationResult(entry *issuedInvite, reused bool) map[string]any {
	binding := bindingLabel(d.state)
	if d.claudeBinding.Ready(time.Now()) {
		binding = "native_claude"
	}
	result := map[string]any{"url": entry.URL, "expires_at": entry.Descriptor.ExpiresAt, "scope": "coauthor", "binding": binding, "reused": reused, "id": entry.Descriptor.InviteID}
	if d.inviteCap > 0 {
		result["relay_max_lifetime"] = d.inviteCap
	}
	return result
}

func (d *Daemon) listInvites() []IssuedInvitation {
	now := unixNow()
	list := []IssuedInvitation{}
	for id, entry := range d.state.Issued {
		if entry.Terminal || entry.Sent || entry.Descriptor.ExpiresAt <= now {
			continue
		}
		state := "unused"
		if entry.Claim != nil {
			state = "claimed"
		}
		list = append(list, IssuedInvitation{ID: id, NotBefore: entry.Descriptor.NotBefore, ExpiresAt: entry.Descriptor.ExpiresAt, State: state})
	}
	sort.Slice(list, func(i, j int) bool { return list[i].ExpiresAt > list[j].ExpiresAt })
	return list
}

// revokeInvite withdraws a link locally first, so a relay outage can never
// leave a link the owner believes dead still claimable here; the relay
// copy is withdrawn as well and reported.
func (d *Daemon) revokeInvite(ctx context.Context, id string) (any, error) {
	entry := d.state.Issued[id]
	if entry == nil {
		return nil, fault.New(store.NotFound)
	}
	if entry.Sent {
		return nil, localipc.Explain("conflict", "rejected", "This invitation was already used to establish a link; revoke the link with peerturn grants revoke instead.")
	}
	entry.Terminal, entry.Failure = true, "revoked"
	entry.URL, entry.Descriptor.EnrollmentVoucher = "", ""
	entry.publication = invitePublication{}
	if err := d.db.RevokeInvite(ctx, id); err != nil && !fault.IsCode(err, store.NotFound) {
		return nil, err
	}
	if err := d.save(); err != nil {
		return nil, err
	}
	relay := "revoked"
	if err := networkDo(ctx, d, func() error { return d.relay.RevokeInvite(ctx, id) }); err != nil {
		relay = localipc.PublicError(err).Code
	}
	d.logger().Info("invitation revoked", "invite", shortID(id), "relay", relay)
	return map[string]string{"state": "revoked", "relay": relay}, nil
}
