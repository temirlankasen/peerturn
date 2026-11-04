package daemon

import (
	"context"
	"crypto/ed25519"

	peercrypto "github.com/temirlankasen/peerturn/internal/crypto"
	"github.com/temirlankasen/peerturn/internal/fault"
	"github.com/temirlankasen/peerturn/internal/identity"
	"github.com/temirlankasen/peerturn/internal/invite"
)

// renewalMargin keeps a new invitation inside the certificate that signs it.
const renewalMargin = invite.MaxLifetime

// renewChain reissues the session certificate once it has lapsed. Without it
// a profile stops inviting, joining and even starting a day after init. Every
// grant and packet is capped at the old certificate's expiry, so renewing then
// cuts nothing still valid. margin renews that much earlier and is only for
// callers that proved no grant is still live.
func renewChain(s *clientState, now, margin int64) (bool, error) {
	cert, _, err := historicalBinding(s.Chain)
	if err != nil {
		return false, err
	}
	if now+margin < cert.ExpiresAt {
		return false, nil
	}
	return true, reissueSession(s, now)
}

// reissueSession keeps the root, device, session ID and keys. The next session
// epoch is a new binding, so the expiring certificate's grants and sequence
// channels never carry over; peers rebuild the pair by a new invitation.
func reissueSession(s *clientState, now int64) error {
	root := identity.RootFingerprint(s.Chain.RootPublicKey)
	cert, _, err := historicalBinding(s.Chain)
	if err != nil {
		return err
	}
	raw, err := peercrypto.Verify(s.Chain.Device, root, peercrypto.DeviceType, s.Chain.RootPublicKey)
	if err != nil {
		return err
	}
	device, err := identity.DecodeDevice(raw)
	if err != nil {
		return err
	}
	chain := s.Chain
	if now < device.NotBefore || now+identity.SessionMaxLifetime > device.ExpiresAt {
		if len(s.RootSeed) != ed25519.SeedSize {
			return fault.New(fault.Expired)
		}
		device.NotBefore, device.ExpiresAt = now, now+identity.DeviceMaxLifetime
		if chain.Device, err = identity.IssueDevice(device, ed25519.NewKeyFromSeed(s.RootSeed)); err != nil {
			return err
		}
	}
	if len(s.DeviceSeed) != ed25519.SeedSize {
		return fault.New(fault.Expired)
	}
	cert.SessionEpoch++
	cert.NotBefore, cert.ExpiresAt = now, now+identity.SessionMaxLifetime
	if chain.Session, err = identity.IssueSession(cert, ed25519.NewKeyFromSeed(s.DeviceSeed)); err != nil {
		return err
	}
	if _, err = identity.VerifyChain(chain, root, now); err != nil {
		return err
	}
	s.Chain = chain
	return nil
}

// renewIdentity applies renewChain to a running daemon. The new binding is
// recorded before any grant or invitation can name it. Before an invitation or
// join (early) it renews within renewalMargin, so the new link is not cut
// short, but only while no grant is live: changing the epoch would make the
// daemon reject packets those grants still authorize. A day-long certificate
// from before 0.1.9 is renewed on the owner's invitation or join regardless:
// its grants lapse within that day anyway, and the new link would otherwise
// break again at its expiry.
func (d *Daemon) renewIdentity(ctx context.Context, early bool) error {
	margin := int64(0)
	if early {
		live, err := d.liveGrants(ctx)
		if err != nil {
			return err
		}
		switch {
		case d.cert.ExpiresAt-d.cert.NotBefore < identity.SessionMaxLifetime/2:
			margin = identity.SessionMaxLifetime
		case !live:
			margin = renewalMargin
		}
	}
	next := d.state
	renewed, err := renewChain(&next, unixNow(), margin)
	if err != nil || !renewed {
		return err
	}
	cert, err := identity.VerifyChain(next.Chain, identity.RootFingerprint(next.Chain.RootPublicKey), unixNow())
	if err != nil {
		return err
	}
	if err = d.db.InstallSession(ctx, next.Chain, cert.IdentityID, unixNow()); err != nil {
		return err
	}
	if err = d.db.BindSession(ctx, cert.Binding, bindingContext(next)); err != nil {
		return err
	}
	d.state.Chain, d.cert = next.Chain, cert
	return d.save()
}

func (d *Daemon) liveGrants(ctx context.Context) (bool, error) {
	grants, err := d.db.ListGrants(ctx)
	if err != nil {
		return false, err
	}
	now := unixNow()
	for _, g := range grants {
		if !g.Revoked && g.ExpiresAt > now {
			return true, nil
		}
	}
	return false, nil
}
