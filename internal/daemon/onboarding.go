package daemon

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"net/url"
	"sort"
	"time"

	peercrypto "github.com/temirlankasen/peerturn/internal/crypto"
	"github.com/temirlankasen/peerturn/internal/envelope"
	"github.com/temirlankasen/peerturn/internal/fault"
	"github.com/temirlankasen/peerturn/internal/grant"
	"github.com/temirlankasen/peerturn/internal/identity"
	"github.com/temirlankasen/peerturn/internal/invite"
	"github.com/temirlankasen/peerturn/internal/localipc"
	"github.com/temirlankasen/peerturn/internal/relayclient"
)

// A built-in profile pin is used only by already installed preview binaries.
// This is not a publisher signature or a download/bootstrap trust decision.
var previewManifest = peercrypto.Digest([]byte("peerturn/m25/installed-preview/v1"))

func (d *Daemon) createInvite(ctx context.Context) (any, error) {
	if d.activeInvites() >= 32 {
		return nil, fault.New(fault.BudgetExceeded)
	}
	if err := d.enroll(ctx, ""); err != nil {
		return nil, err
	}
	now := unixNow()
	voucher := randomID() + randomID()
	descriptor := invite.Descriptor{Version: 1, Inviter: d.state.Chain, ForwardScope: grant.Coauthor, ReturnScope: grant.Coauthor, RelayOrigin: d.state.RelayURL, NotBefore: now, ExpiresAt: min(now+invite.MaxLifetime, d.cert.ExpiresAt), MaxUses: 1, PublisherManifestHash: previewManifest, EnrollmentVoucher: voucher, RoutingKey: d.routingPublic(), MailboxKey: d.routingPublic()}
	link, packet, err := invite.Create(descriptor, d.signingKey(), now)
	if err != nil {
		return nil, err
	}
	descriptor.InviteID = link.ID()
	entry := &issuedInvite{URL: link.SecretURL(), Descriptor: descriptor, Packet: packet}
	d.state.Issued[link.ID()] = entry
	if err = d.save(); err != nil {
		return nil, err
	}
	if err = d.db.CreateInvite(ctx, link.ID(), descriptor.ExpiresAt); err != nil {
		return nil, err
	}
	if err = d.publishInvite(ctx, entry); err != nil {
		return nil, err
	}
	d.signal()
	binding := bindingLabel(d.state)
	if d.claudeBinding.Ready(time.Now()) {
		binding = "native_claude"
	}
	return map[string]any{"url": link.SecretURL(), "expires_at": descriptor.ExpiresAt, "scope": "coauthor", "binding": binding}, nil
}

func (d *Daemon) publishInvite(ctx context.Context, i *issuedInvite) error {
	link, err := invite.ParseLink(i.URL)
	if err != nil {
		return err
	}
	key, err := inviteProofKey(link)
	if err != nil {
		return err
	}
	return networkDo(ctx, d, func() error {
		return d.relay.CreateInvite(ctx, relayclient.InviteRequest{ID: link.ID(), ProofKey: publicKey(key[32:]), Packet: i.Packet, ExpiresAt: i.Descriptor.ExpiresAt, Voucher: i.Descriptor.EnrollmentVoucher})
	})
}

func inviteProofKey(link invite.Link) (ed25519.PrivateKey, error) {
	// SecretURL is the sole deliberate secret export; parsing remains local.
	u, err := url.Parse(link.SecretURL())
	if err != nil {
		return nil, fault.New(fault.InvalidInvite)
	}
	values, err := url.ParseQuery(u.Fragment)
	if err != nil {
		return nil, fault.New(fault.InvalidInvite)
	}
	seed, err := base64.RawURLEncoding.Strict().DecodeString(values.Get("ik"))
	if err != nil || len(seed) != 32 {
		return nil, fault.New(fault.InvalidInvite)
	}
	defer clear(seed)
	return ed25519.NewKeyFromSeed(seed), nil
}

func (d *Daemon) join(ctx context.Context, raw string) (any, error) {
	return d.joinWithCapability(ctx, raw, "")
}

func (d *Daemon) joinWithCapability(ctx context.Context, raw, capability string) (any, error) {
	if err := d.checkInvitationOrigin(raw, capability); err != nil {
		return nil, err
	}
	link, err := invite.ParseLink(raw)
	if err != nil {
		return nil, err
	}
	// A pasted link never redirects the daemon to another configured relay.
	if link.FetchURL() != d.state.RelayURL+"/i/"+link.ID() {
		return nil, fault.New(fault.AudienceMismatch)
	}
	if old := d.state.Joining[link.ID()]; old != nil {
		if old.URL != raw && old.URLDigest != peercrypto.Digest([]byte(raw)) {
			return nil, fault.New(fault.Conflict)
		}
		d.signal()
		return map[string]string{"state": joinState(old), "peer": peerID(old.Descriptor.Inviter)}, nil
	}
	if d.activeJoins() >= 32 {
		return nil, fault.New(fault.BudgetExceeded)
	}
	remote, err := networkCall(ctx, d, func() (relayclient.Invite, error) { return d.relay.GetInvite(ctx, link.ID()) })
	if err != nil {
		return nil, err
	}
	descriptor, inviter, err := invite.OpenDescriptor(link, remote.Packet, previewManifest, unixNow())
	if err != nil {
		return nil, err
	}
	if descriptor.ForwardScope != grant.Coauthor || descriptor.ReturnScope != grant.Coauthor || len(descriptor.RoutingKey) != 32 || len(descriptor.MailboxKey) != 32 || !equalBytes(descriptor.RoutingKey, descriptor.MailboxKey) {
		return nil, fault.New(fault.ScopeDenied)
	}
	if err = d.checkPeerBinding(descriptor.Inviter, descriptor.RoutingKey); err != nil {
		return nil, err
	}
	key, err := inviteProofKey(link)
	if err != nil {
		return nil, err
	}
	if remote.ProofKey != publicKey(key[32:]) {
		return nil, fault.New(fault.InvalidInvite)
	}
	// Relay I/O releases mu and may outlast the native lease. Recheck before
	// creating any claim, grant, or retained onboarding authority.
	if capability != "" && d.state.Codex != nil {
		if err = d.checkNative(ctx, d.state.Codex.ThreadID); err != nil {
			return nil, err
		}
	}
	if err = d.checkInvitationOrigin(raw, capability); err != nil {
		return nil, err
	}
	now := unixNow()
	returnGrant, err := d.issueGrant(inviter)
	if err != nil {
		return nil, err
	}
	claimID, err := hex.DecodeString(randomID())
	if err != nil {
		return nil, err
	}
	// Independent 128-bit claim ID; its encoding is the invitation profile, not a message ID.
	claim := invite.Claim{Version: 1, InviteID: link.ID(), ClaimID: base64.RawURLEncoding.EncodeToString(claimID), Claimant: d.state.Chain, Audience: inviter.Binding, RequestedScope: grant.Coauthor, ReturnGrant: returnGrant, RoutingKey: d.routingPublic(), MailboxKey: d.routingPublic(), NotBefore: now, ExpiresAt: descriptor.ExpiresAt}
	packet, err := invite.CreateClaim(link, descriptor, claim, d.signingKey(), 1, now)
	if err != nil {
		return nil, err
	}
	d.state.Joining[link.ID()] = &joiningInvite{URL: raw, Descriptor: descriptor, Claim: claim, Packet: packet}
	d.state.Peers[peerID(descriptor.Inviter)] = &peerState{Chain: descriptor.Inviter, RoutingKey: descriptor.RoutingKey, Incoming: returnGrant}
	if err = d.installGrant(ctx, returnGrant, d.state.Chain, descriptor.Inviter); err != nil {
		return nil, err
	}
	if err = d.save(); err != nil {
		return nil, err
	}
	d.signal()
	return map[string]string{"state": "waiting_inviter", "peer": peerID(descriptor.Inviter)}, nil
}

func joinState(j *joiningInvite) string {
	if j.Complete {
		return "connected"
	}
	return "waiting_inviter"
}
func peerID(c identity.Chain) string { return peercrypto.Digest([]byte(c.Session)) }

func (d *Daemon) issueGrant(subject identity.SessionCertificate) (string, error) {
	now := unixNow()
	return grant.Issue(grant.Grant{Version: 1, GrantID: randomID(), Issuer: d.cert.Binding, Subject: subject.Binding, Scope: grant.Coauthor, GrantEpoch: 1, NotBefore: now, ExpiresAt: min(now+grant.DefaultLifetime, d.cert.ExpiresAt, subject.ExpiresAt), Limits: grant.DefaultLimits(), ReplyRoute: &subject.Binding}, d.signingKey())
}

func (d *Daemon) installGrant(ctx context.Context, signed string, recipient, sender identity.Chain) error {
	return d.db.InstallGrant(ctx, envelope.OpenOptions{RecipientChain: recipient, RecipientPin: identity.RootFingerprint(recipient.RootPublicKey), SenderChain: sender, SenderPin: identity.RootFingerprint(sender.RootPublicKey), SignedGrant: signed, CurrentGrantEpoch: 1, Now: unixNow()})
}

func (d *Daemon) enroll(ctx context.Context, voucher string) error {
	if d.state.Enrolled {
		return nil
	}
	_, err := networkCall(ctx, d, func() (string, error) {
		return d.relay.EnrollMailbox(ctx, relayclient.MailboxRequest{ID: d.mailbox(), ReadKey: publicKey(d.routingPublic()), Voucher: voucher})
	})
	if err != nil {
		return err
	}
	d.state.Enrolled = true
	return d.save()
}

func (d *Daemon) allowPeer(ctx context.Context, peer *peerState) error {
	cert, err := identity.VerifyChain(peer.Chain, identity.RootFingerprint(peer.Chain.RootPublicKey), unixNow())
	if err != nil {
		return err
	}
	return networkDo(ctx, d, func() error {
		return d.relay.PutPermit(ctx, d.mailbox(), routePermit(peer.RoutingKey), relayclient.Permit{WriterKey: publicKey(peer.RoutingKey), Epoch: 1, ExpiresAt: min(d.cert.ExpiresAt, cert.ExpiresAt)})
	})
}

func (d *Daemon) peers() []Peer {
	peers := make([]Peer, 0, len(d.state.Peers))
	for id, p := range d.state.Peers {
		state := "waiting_inviter"
		if p.HelloReceived && p.HelloSent {
			state = "connected"
		}
		if p.Revoked {
			state = "revoked"
		}
		if p.IncomingRevoked {
			state = "revoked"
		}
		if p.SequenceBlocked && !p.IncomingRevoked && !p.Revoked {
			state = "needs_rebind"
		}
		cert, err := identity.VerifyChain(p.Chain, identity.RootFingerprint(p.Chain.RootPublicKey), unixNow())
		if err != nil {
			state = "expired"
		}
		peers = append(peers, Peer{ID: id, Fingerprint: identity.RootFingerprint(p.Chain.RootPublicKey), State: state, Scope: "coauthor", ExpiresAt: cert.ExpiresAt})
	}
	sort.Slice(peers, func(i, j int) bool { return peers[i].ID < peers[j].ID })
	return peers
}

func (d *Daemon) sessionCheck(r localipc.Request) error {
	if r.Capability == "" && r.Session == "" {
		switch r.Method {
		case "status", "doctor", "stop", "delivery.resolve", "session.unbind":
			return nil
		}
	}
	if r.Session == "" || r.Session != d.state.Label {
		return localipc.NewError("unbound", "unbound")
	}
	if r.Capability != "" && !d.validCapability(r.Capability, r.Session) {
		return localipc.NewError("unbound", "unbound")
	}
	return nil
}
