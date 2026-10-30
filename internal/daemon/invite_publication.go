package daemon

import (
	"context"
	"time"

	"github.com/temirlankasen/peerturn/internal/codec"
	peercrypto "github.com/temirlankasen/peerturn/internal/crypto"
	"github.com/temirlankasen/peerturn/internal/fault"
	"github.com/temirlankasen/peerturn/internal/invite"
)

// One transient record per retained invite shares the journal's existing bounds.
// It confirms only exact publication bytes, never claim admission or authority.
type invitePublication struct {
	key       string
	confirmed bool
	failure   error
	retryAt   time.Time
	delay     time.Duration
}

func (d *Daemon) invitePublication(i *issuedInvite) (*invitePublication, error) {
	if unixNow() >= i.Descriptor.ExpiresAt {
		i.publication = invitePublication{}
		return nil, fault.New(fault.Expired)
	}
	raw, err := codec.Canonical(struct {
		Origin     string            `json:"origin"`
		Signer     string            `json:"signer"`
		URL        string            `json:"url"`
		Descriptor invite.Descriptor `json:"descriptor"`
		Packet     invite.Packet     `json:"packet"`
	}{d.state.RelayURL, publicKey(d.routingPublic()), i.URL, i.Descriptor, i.Packet})
	if err != nil {
		i.publication = invitePublication{}
		return nil, err
	}
	key := peercrypto.Digest(raw)
	if i.publication.key != key {
		i.publication = invitePublication{key: key}
	}
	return &i.publication, nil
}

func (d *Daemon) ensureInvitePublished(ctx context.Context, i *issuedInvite) error {
	p, err := d.invitePublication(i)
	if err != nil {
		return err
	}
	if err = p.deferred(time.Now()); err != nil {
		return err
	}
	if p.confirmed && d.state.Enrolled {
		return nil
	}
	return d.publishInvite(ctx, i)
}

func (p *invitePublication) deferred(now time.Time) error {
	if now.Before(p.retryAt) {
		return p.failure
	}
	return nil
}

func (p *invitePublication) failed(err error, now time.Time) {
	p.confirmed = false
	if terminalOnboarding(err, false) || retryCustody(err) {
		*p = invitePublication{}
		return
	}
	// Reporting a deferred attempt must neither postpone it nor clear its cause.
	if now.Before(p.retryAt) {
		return
	}
	p.failure = err
	p.delay = min(2*time.Minute, max(2*time.Second, 2*p.delay))
	p.retryAt = now.Add(p.delay)
}
