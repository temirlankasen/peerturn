package invite

import (
	"bytes"
	"strings"
	"testing"

	ptcrypto "github.com/temirlankasen/peerturn/internal/crypto"
	"github.com/temirlankasen/peerturn/internal/fault"
	"github.com/temirlankasen/peerturn/internal/grant"
)

func TestClaimRequiresInviterAndClaimantSessionKeys(t *testing.T) {
	f := newInviteFixture(t)
	claim, err := OpenClaim(f.link, f.descriptor, f.claimWire, f.inviter.key, f.claimant.cert.IdentityID, 1, testNow)
	if err != nil || !bytes.Equal(canonical(t, claim), canonical(t, f.claim)) {
		t.Fatal("claim failed round trip", err)
	}
	if _, err := OpenClaim(f.link, f.descriptor, f.claimWire, f.claimant.key, f.claimant.cert.IdentityID, 1, testNow); !fault.IsCode(err, fault.AudienceMismatch) {
		t.Fatal("claim decrypted with another session key", err)
	}
	if _, err := OpenClaim(f.link, f.descriptor, f.claimWire, f.inviter.key, strings.Repeat("b", 64), 1, testNow); !fault.IsCode(err, fault.NeedsVerify) {
		t.Fatal("unpinned claimant accepted", err)
	}
	if _, err := OpenClaim(f.link, f.descriptor, f.claimWire, f.inviter.key, f.claimant.cert.IdentityID, 2, testNow); !fault.IsCode(err, fault.Revoked) {
		t.Fatal("stale return grant accepted", err)
	}
	if _, err := CreateClaim(f.link, f.descriptor, f.claim, f.inviter.sign, 1, testNow); !fault.IsCode(err, fault.InvalidSignature) {
		t.Fatal("claim signed by the wrong session accepted", err)
	}
}

func TestEveryClaimFieldIsAuthenticated(t *testing.T) {
	f := newInviteFixture(t)
	compact := claimCompact(t, f)
	cases := map[string]func(*Claim){
		"version": func(c *Claim) { c.Version++ }, "invite": func(c *Claim) { c.InviteID = strings.Repeat("a", 22) },
		"claim":  func(c *Claim) { c.ClaimID = strings.Repeat("a", 22) },
		"root":   func(c *Claim) { c.Claimant.RootPublicKey = bytes.Repeat([]byte{1}, 32) },
		"device": func(c *Claim) { c.Claimant.Device += "x" }, "session": func(c *Claim) { c.Claimant.Session += "x" },
		"audience_identity": func(c *Claim) { c.Audience.IdentityID = strings.Repeat("b", 64) },
		"audience_device":   func(c *Claim) { c.Audience.DeviceID += "x" }, "audience_session": func(c *Claim) { c.Audience.SessionID += "x" },
		"audience_device_epoch": func(c *Claim) { c.Audience.DeviceEpoch++ }, "audience_session_epoch": func(c *Claim) { c.Audience.SessionEpoch++ },
		"audience_key_epoch": func(c *Claim) { c.Audience.KeyEpoch++ }, "audience_key": func(c *Claim) { c.Audience.SigningKey = bytes.Repeat([]byte{1}, 32) },
		"scope": func(c *Claim) { c.RequestedScope = grant.Ask }, "return_grant": func(c *Claim) { c.ReturnGrant += "x" },
		"routing": func(c *Claim) { c.RoutingKey = bytes.Repeat([]byte{1}, 32) }, "mailbox": func(c *Claim) { c.MailboxKey = bytes.Repeat([]byte{1}, 32) },
		"not_before": func(c *Claim) { c.NotBefore++ }, "expires": func(c *Claim) { c.ExpiresAt++ },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			claim := f.claim
			mutate(&claim)
			packet := sealClaim(t, f, compactWithPayload(t, compact, claim))
			if _, err := OpenClaim(f.link, f.descriptor, packet, f.inviter.key, f.claimant.cert.IdentityID, 1, testNow); !fault.IsCode(err, fault.InvalidSignature) {
				t.Fatal("tampered session-signed claim accepted", err)
			}
		})
	}
}

func TestEveryExternalProofFieldIsAuthenticatedAndBound(t *testing.T) {
	f := newInviteFixture(t)
	cases := map[string]func(*claimProof){
		"version": func(p *claimProof) { p.Version++ }, "invite": func(p *claimProof) { p.InviteID = strings.Repeat("a", 22) },
		"claim": func(p *claimProof) { p.ClaimID = strings.Repeat("a", 22) }, "hash": func(p *claimProof) { p.CiphertextHash = strings.Repeat("b", 64) },
		"expiry": func(p *claimProof) { p.ExpiresAt++ },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			proof := decodeProof(t, f.claimWire)
			mutate(&proof)
			packet := f.claimWire
			packet.Proof = compactWithPayload(t, packet.Proof, proof)
			if _, err := OpenClaim(f.link, f.descriptor, packet, f.inviter.key, f.claimant.cert.IdentityID, 1, testNow); !fault.IsCode(err, fault.InvalidSignature) {
				t.Fatal("external proof signature failed to bind field", err)
			}
			packet = signProof(t, f.link, f.claimWire.Packet, proof)
			if _, err := OpenClaim(f.link, f.descriptor, packet, f.inviter.key, f.claimant.cert.IdentityID, 1, testNow); !fault.IsCode(err, fault.InvalidInvite) {
				t.Fatal("valid proof with mismatched binding accepted", err)
			}
		})
	}
	packet := f.claimWire
	packet.Packet.Ciphertext = bytes.Clone(packet.Packet.Ciphertext)
	packet.Packet.Ciphertext[0] ^= 1
	if _, err := OpenClaim(f.link, f.descriptor, packet, f.inviter.key, f.claimant.cert.IdentityID, 1, testNow); !fault.IsCode(err, fault.InvalidInvite) {
		t.Fatal("proof accepted changed ciphertext", err)
	}
}

func TestClaimRejectsInvalidScopeAudienceAndTime(t *testing.T) {
	f := newInviteFixture(t)
	cases := []struct {
		mutate func(*Claim)
		code   fault.Code
	}{
		{func(c *Claim) { c.RequestedScope = "receipt" }, fault.InvalidGrant},
		{func(c *Claim) { c.Audience.SessionID = "other-session" }, fault.AudienceMismatch},
		{func(c *Claim) { c.ExpiresAt = f.descriptor.ExpiresAt + 1 }, fault.InvalidInvite},
		{func(c *Claim) { c.NotBefore = testNow + 1 }, fault.NotYetValid},
		{func(c *Claim) { c.ExpiresAt = testNow }, fault.InvalidInvite},
	}
	for i, test := range cases {
		claim := f.claim
		test.mutate(&claim)
		if _, err := CreateClaim(f.link, f.descriptor, claim, f.claimant.sign, 1, testNow); !fault.IsCode(err, test.code) {
			t.Fatalf("case %d: %v", i, err)
		}
	}
	if _, err := OpenClaim(f.link, f.descriptor, f.claimWire, f.inviter.key, f.claimant.cert.IdentityID, 1, f.claim.ExpiresAt); !fault.IsCode(err, fault.Expired) {
		t.Fatal("expired claim accepted", err)
	}
}

func TestReturnGrantMustBeRecipientIssuedAndDirectional(t *testing.T) {
	f := newInviteFixture(t)
	g := grant.Grant{Version: 1, GrantID: "wrong-return", Issuer: f.inviter.cert.Binding, Subject: f.claimant.cert.Binding,
		Scope: grant.Coauthor, GrantEpoch: 1, NotBefore: testNow, ExpiresAt: testNow + 300, Limits: grant.DefaultLimits()}
	wrongDirection, err := grant.Issue(g, f.inviter.sign)
	if err != nil {
		t.Fatal(err)
	}
	claim := f.claim
	claim.ReturnGrant = wrongDirection
	if _, createErr := CreateClaim(f.link, f.descriptor, claim, f.claimant.sign, 1, testNow); !fault.IsCode(createErr, fault.InvalidSignature) {
		t.Fatal("forward grant accepted as the reverse grant", createErr)
	}
	claim.ReturnGrant = f.claim.ReturnGrant
	parts := strings.Split(claim.ReturnGrant, ".")
	parts[2] = strings.Repeat("a", len(parts[2]))
	claim.ReturnGrant = strings.Join(parts, ".")
	if _, createErr := CreateClaim(f.link, f.descriptor, claim, f.claimant.sign, 1, testNow); !fault.IsCode(createErr, fault.InvalidSignature) {
		t.Fatal("forged return grant accepted", createErr)
	}
	// The invite proof capability alone cannot replace a claimant session signature.
	signed, err := ptcrypto.Sign(canonical(t, f.claim), f.link.ID(), ptcrypto.ClaimType, f.inviter.sign)
	if err != nil {
		t.Fatal(err)
	}
	packet := sealClaim(t, f, signed)
	if _, err := OpenClaim(f.link, f.descriptor, packet, f.inviter.key, f.claimant.cert.IdentityID, 1, testNow); !fault.IsCode(err, fault.InvalidSignature) {
		t.Fatal("invitation capability escalated to session authority", err)
	}
}
