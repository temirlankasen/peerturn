package daemon

import (
	"context"
	"crypto/ed25519"
	"errors"
	"slices"
	"time"

	"github.com/temirlankasen/peerturn/internal/codec"
	"github.com/temirlankasen/peerturn/internal/fault"
	"github.com/temirlankasen/peerturn/internal/identity"
	"github.com/temirlankasen/peerturn/internal/invite"
	"github.com/temirlankasen/peerturn/internal/protocol"
	"github.com/temirlankasen/peerturn/internal/relayclient"
)

func (d *Daemon) syncJoins(ctx context.Context) error {
	var result error
	for _, j := range d.state.Joining {
		if j.Terminal {
			continue
		}
		err := d.syncJoin(ctx, j)
		err = d.onboardingResult(&j.Failure, &j.Terminal, err, j.Complete)
		result = onboardingErrors(result, err)
		if retryCustody(err) {
			return result
		}
	}
	return result
}

func (d *Daemon) syncJoin(ctx context.Context, j *joiningInvite) error {
	if !j.Complete && (!j.Claimed || !j.Enrolled) && unixNow() >= j.Descriptor.ExpiresAt {
		return fault.New(fault.Expired)
	}
	// A saved join can precede its separate store commit. Restore its verified
	// recipient-issued grant before exposing a claim or acknowledging a hello.
	if err := d.installGrant(ctx, j.Claim.ReturnGrant, d.state.Chain, j.Descriptor.Inviter); err != nil {
		return err
	}
	p := d.state.Peers[peerID(j.Descriptor.Inviter)]
	if p == nil {
		return invalidHandshake()
	}
	if j.Complete {
		if p.Outgoing != "" {
			if err := d.installGrant(ctx, p.Outgoing, p.Chain, d.state.Chain); err != nil {
				return err
			}
		}
		if !j.AckSent && j.Ack != nil {
			if _, err := networkCall(ctx, d, func() (relayclient.Receipt, error) { return d.relay.Send(ctx, *j.Ack) }); err != nil {
				return err
			}
			j.AckSent = true
			return d.save()
		}
		return nil
	}
	if !j.Claimed {
		if _, err := networkCall(ctx, d, func() (relayclient.ClaimReceipt, error) { return d.relay.ClaimInvite(ctx, j.Claim.InviteID, j.Packet) }); err != nil {
			return err
		}
		j.Claimed = true
		if err := d.save(); err != nil {
			return err
		}
	}
	if err := d.enroll(ctx, j.Descriptor.EnrollmentVoucher); err != nil {
		return err
	}
	if !j.Enrolled {
		if err := d.allowPeer(ctx, p); err != nil {
			return err
		}
		j.Enrolled = true
		return d.save()
	}
	return nil
}

func (d *Daemon) syncInvites(ctx context.Context) error {
	var result error
	for _, i := range d.state.Issued {
		if i.Terminal {
			i.publication = invitePublication{}
			continue
		}
		err := d.syncInvite(ctx, i)
		if err != nil && i.Claim == nil && !i.Sent {
			i.publication.failed(err, time.Now())
		}
		err = d.onboardingResult(&i.Failure, &i.Terminal, err, i.Claim != nil)
		if i.Terminal || i.Sent || i.Claim != nil {
			i.publication = invitePublication{}
		}
		result = onboardingErrors(result, err)
		if retryCustody(err) {
			return result
		}
	}
	return result
}

func (d *Daemon) syncInvite(ctx context.Context, i *issuedInvite) error {
	if i.Sent {
		i.publication = invitePublication{}
		return nil
	}
	if i.Claim != nil {
		i.publication = invitePublication{}
		return d.finishInvite(ctx, i)
	}
	if unixNow() >= i.Descriptor.ExpiresAt {
		i.publication = invitePublication{}
		return fault.New(fault.Expired)
	}
	if err := d.db.CreateInvite(ctx, i.Descriptor.InviteID, i.Descriptor.ExpiresAt); err != nil {
		return err
	}
	if err := d.ensureInvitePublished(ctx, i); err != nil {
		return err
	}
	link, err := invite.ParseLink(i.URL)
	if err != nil {
		return err
	}
	key, err := inviteProofKey(link)
	if err != nil {
		return err
	}
	client, err := relayclient.New(relayclient.Options{URL: d.state.RelayURL, Key: key, HTTPClient: d.options.HTTPClient, ExpectedRelayKey: d.state.PinnedRelayKey})
	if err != nil {
		return err
	}
	remote, err := networkCall(ctx, d, func() (relayclient.Invite, error) { return client.GetInvite(ctx, link.ID()) })
	if err != nil {
		return err
	}
	if remote.ClaimPacket == nil {
		return nil
	}
	// GET can return a claim after publication ownership, mailbox or revocation
	// changed. Revalidate exact custody before validating a new claim for effects.
	if err = d.publishInvite(ctx, i); err != nil {
		return err
	}
	if unixNow() >= i.Descriptor.ExpiresAt {
		i.publication = invitePublication{}
		return fault.New(fault.Expired)
	}
	claim, err := invite.OpenClaim(link, i.Descriptor, *remote.ClaimPacket, d.key, "", 1, unixNow())
	if err != nil {
		return err
	}
	cert, err := identity.VerifyChain(claim.Claimant, claim.ClaimantFingerprint(), unixNow())
	if err != nil {
		return err
	}
	if err = d.checkPeerBinding(claim.Claimant, claim.RoutingKey); err != nil {
		return err
	}
	forward, err := d.issueGrant(cert)
	if err != nil {
		return err
	}
	_, digest, err := invite.ClaimRef(link, *remote.ClaimPacket)
	if err != nil {
		return err
	}
	p := &peerState{Chain: claim.Claimant, RoutingKey: claim.RoutingKey, Incoming: forward, Outgoing: claim.ReturnGrant}
	if !equalBytes(claim.RoutingKey, claim.MailboxKey) {
		return invalidHandshake()
	}
	response, err := d.handshakePacket(p, handshake{InviteID: claim.InviteID, ClaimID: claim.ClaimID, ForwardGrant: forward, ReturnGrantHash: grantHash(claim.ReturnGrant), Hello: true})
	if err != nil {
		return err
	}
	i.Claim = &claim
	i.publication = invitePublication{}
	i.ClaimDigest = digest
	i.ForwardGrant = forward
	i.Response = &response
	d.state.Peers[peerID(claim.Claimant)] = p
	if err = d.save(); err != nil {
		return err
	}
	return d.finishInvite(ctx, i)
}

func (d *Daemon) onboardingResult(failure *string, terminal *bool, err error, retained bool) error {
	code, stop := "", false
	if err != nil {
		code, stop = safeCode(err), terminalOnboarding(err, retained)
	}
	if *failure == code && *terminal == stop {
		return err
	}
	*failure, *terminal = code, stop
	return onboardingErrors(err, d.save())
}

func terminalOnboarding(err error, retained bool) bool {
	if typed, ok := errors.AsType[*fault.Error](err); ok {
		if slices.Contains([]fault.Code{fault.Expired, fault.Revoked, fault.InvalidInvite, fault.AlreadyUsed,
			fault.InvalidEncoding, fault.InvalidSignature, fault.DecryptionFailed,
			fault.InvalidCertificate, fault.AudienceMismatch, fault.InvalidGrant,
			fault.EpochMismatch, fault.ScopeDenied, fault.Conflict, fault.UnsupportedVersion,
			fault.TooLarge, fault.NeedsVerify}, typed.Code) {
			return true
		}
	}
	if remote, ok := errors.AsType[*relayclient.Error](err); ok {
		if remote.Status == 410 || remote.Code == "invalid_invite_proof" || remote.Code == "claim_reader_mismatch" {
			return true
		}
		// A valid consumed claim can race the joiner's mailbox enrollment.
		// Its exact response retries 403/404 instead of admitting a new claim.
		return !retained && (remote.Status == 400 || remote.Status == 403 || remote.Status == 404 || remote.Status == 409 || remote.Status == 413 || remote.Status == 426)
	}
	return false
}

func onboardingErrors(first, next error) error {
	// fault.IsCode observes the first typed fault in an errors.Join tree. Put
	// persistence failures first so a terminal invite cannot mask the failure fence.
	if retryCustody(next) {
		return errors.Join(next, first)
	}
	return errors.Join(first, next)
}

func (d *Daemon) finishInvite(ctx context.Context, i *issuedInvite) error {
	raw, err := codec.Canonical(i.Response)
	if err != nil {
		return err
	}
	saved, err := d.db.ConsumeInvite(ctx, i.Descriptor.InviteID, i.Claim.ClaimID, i.ClaimDigest, raw, unixNow())
	if err != nil {
		return err
	}
	packet, err := protocol.DecodePacket(saved)
	if err != nil {
		return err
	}
	p := d.state.Peers[peerID(i.Claim.Claimant)]
	if p == nil {
		return invalidHandshake()
	}
	if err = d.installGrant(ctx, p.Incoming, d.state.Chain, p.Chain); err != nil {
		return err
	}
	if err = d.installGrant(ctx, p.Outgoing, p.Chain, d.state.Chain); err != nil {
		return err
	}
	if err = d.allowPeer(ctx, p); err != nil {
		return err
	}
	if _, err = networkCall(ctx, d, func() (relayclient.Receipt, error) { return d.relay.Send(ctx, packet) }); err != nil {
		return err
	}
	i.Sent = true
	p.HelloSent = true
	return d.save()
}

// Explicit conversion keeps routing identities separate from session signing keys.
func routePrivate(seed []byte) ed25519.PrivateKey { return ed25519.NewKeyFromSeed(seed) }
