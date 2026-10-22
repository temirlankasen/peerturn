package daemon

import (
	"context"
	"crypto/ecdh"

	"github.com/temirlankasen/peerturn/internal/codec"
	peercrypto "github.com/temirlankasen/peerturn/internal/crypto"
	"github.com/temirlankasen/peerturn/internal/envelope"
	"github.com/temirlankasen/peerturn/internal/fault"
	"github.com/temirlankasen/peerturn/internal/grant"
	"github.com/temirlankasen/peerturn/internal/identity"
	"github.com/temirlankasen/peerturn/internal/protocol"
)

type handshake struct {
	InviteID        string `json:"invite_id"`
	ClaimID         string `json:"claim_id"`
	ForwardGrant    string `json:"forward_grant,omitempty"`
	ReturnGrantHash string `json:"return_grant_hash"`
	Hello           bool   `json:"hello"`
}

func invalidHandshake() error   { return fault.New(fault.InvalidInvite) }
func grantHash(s string) string { return grant.Hash(s) }

func (d *Daemon) handshakePacket(p *peerState, hello handshake) (protocol.EncryptedPacket, error) {
	cert, err := identity.VerifyChain(p.Chain, identity.RootFingerprint(p.Chain.RootPublicKey), unixNow())
	if err != nil {
		return protocol.EncryptedPacket{}, err
	}
	g, err := grant.Verify(p.Outgoing, cert, d.cert, 1, unixNow())
	if err != nil {
		return protocol.EncryptedPacket{}, err
	}
	body, err := codec.Canonical(hello)
	if err != nil {
		return protocol.EncryptedPacket{}, err
	}
	now := unixNow()
	id := randomID()
	expiry := min(now+3600, g.ExpiresAt)
	m := protocol.MessagePayload{MessageID: id, FromSession: d.cert.SessionID, FromSessionEpoch: d.cert.SessionEpoch, ToSession: cert.SessionID, ToSessionEpoch: cert.SessionEpoch, SenderSeq: 1, GrantID: g.GrantID, GrantEpoch: 1, GrantHash: grant.Hash(p.Outgoing), Kind: protocol.GrantUpdate, BodyUTF8: string(body), CreatedAt: now, ExpiresAt: expiry, ConversationID: randomID(), BudgetID: randomID()}
	h := protocol.RoutingHeader{WireVersion: 1, Suite: protocol.Suite, DstMailbox: routeMailbox(p.RoutingKey), RoutePermitID: routePermit(d.routingPublic()), MessageID: id, RecipientKeyEpoch: cert.KeyEpoch, ExpiresAt: expiry}
	key, err := ecdh.X25519().NewPublicKey(cert.EncryptionKey)
	if err != nil {
		return protocol.EncryptedPacket{}, err
	}
	return envelope.Seal(h, m, d.signingKey(), key)
}

// Bootstrap is a separate typed control restricted to a locally approved pending
// invitation. It cannot become message admission, model input, or arbitrary grants.
func (d *Daemon) acceptHandshake(ctx context.Context, p *peerState, packet protocol.EncryptedPacket, seq uint64, verified *protocol.MessagePayload) (bool, error) {
	aad, err := packet.Header.Canonical()
	if err != nil {
		return false, err
	}
	compact, err := peercrypto.Open(d.key, packet.Enc, aad, packet.Ciphertext)
	if err != nil {
		return false, err
	}
	cert, err := identity.VerifyChain(p.Chain, identity.RootFingerprint(p.Chain.RootPublicKey), unixNow())
	if err != nil {
		return false, err
	}
	raw, err := peercrypto.Verify(string(compact), identity.RootFingerprint(cert.SigningKey), peercrypto.MessageType, cert.SigningKey)
	if err != nil {
		return false, err
	}
	var signed envelope.SignedMessage
	if err = codec.Decode(raw, protocol.MaxPlaintext, &signed); err != nil {
		return false, err
	}
	m := signed.Message
	*verified = m
	if m.Kind != protocol.GrantUpdate {
		return false, nil
	}
	if signed.Header != packet.Header || m.Validate() != nil || packet.Header.DstMailbox != d.mailbox() || packet.Header.RoutePermitID != routePermit(p.RoutingKey) || m.MessageID != packet.Header.MessageID || m.ExpiresAt != packet.Header.ExpiresAt || m.FromSession != cert.SessionID || m.FromSessionEpoch != cert.SessionEpoch || m.ToSession != d.cert.SessionID || m.ToSessionEpoch != d.cert.SessionEpoch || packet.Header.RecipientKeyEpoch != d.cert.KeyEpoch || m.CreatedAt > unixNow() || m.ExpiresAt <= unixNow() {
		return true, invalidHandshake()
	}
	g, err := grant.Verify(p.Incoming, d.cert, cert, 1, unixNow())
	if err != nil {
		return true, err
	}
	if m.GrantID != g.GrantID || m.GrantHash != grant.Hash(p.Incoming) || m.GrantEpoch != 1 || m.CreatedAt < g.NotBefore || m.ExpiresAt > g.ExpiresAt {
		return true, invalidHandshake()
	}
	var hello handshake
	if err = codec.Decode([]byte(m.BodyUTF8), 32<<10, &hello); err != nil || !hello.Hello {
		return true, invalidHandshake()
	}
	wire, err := codec.Canonical(packet)
	if err != nil {
		return true, err
	}
	if j := d.state.Joining[hello.InviteID]; j != nil {
		if peerID(j.Descriptor.Inviter) != peerID(p.Chain) || hello.ClaimID != j.Claim.ClaimID || hello.ReturnGrantHash != grant.Hash(j.Claim.ReturnGrant) || hello.ForwardGrant == "" {
			return true, invalidHandshake()
		}
		forward, verifyErr := grant.Verify(hello.ForwardGrant, cert, d.cert, 1, unixNow())
		if verifyErr != nil {
			return true, verifyErr
		}
		if forward.Scope != grant.Coauthor {
			return true, invalidHandshake()
		}
		if j.Complete && (j.ResponseID != m.MessageID || j.ResponseDigest != peercrypto.Digest(wire)) {
			return true, invalidHandshake()
		}
		if !j.Complete {
			p.Outgoing = hello.ForwardGrant
			if err = d.installGrant(ctx, p.Outgoing, p.Chain, d.state.Chain); err != nil {
				return true, err
			}
			ack, ackErr := d.handshakePacket(p, handshake{InviteID: hello.InviteID, ClaimID: hello.ClaimID, ReturnGrantHash: grant.Hash(j.Claim.ReturnGrant), Hello: true})
			if ackErr != nil {
				return true, ackErr
			}
			j.Complete = true
			j.ResponseID = m.MessageID
			j.ResponseDigest = peercrypto.Digest(wire)
			j.Ack = &ack
			p.HelloReceived = true
			p.HelloSent = true
			if err = d.save(); err != nil {
				return true, err
			}
		}
	} else if i := d.state.Issued[hello.InviteID]; i != nil && i.Claim != nil {
		if peerID(i.Claim.Claimant) != peerID(p.Chain) || hello.ClaimID != i.Claim.ClaimID || hello.ReturnGrantHash != grant.Hash(i.Claim.ReturnGrant) || hello.ForwardGrant != "" {
			return true, invalidHandshake()
		}
		p.HelloReceived = true
		if err = d.save(); err != nil {
			return true, err
		}
	} else {
		return true, invalidHandshake()
	}
	_, err = d.db.CommitTransport(ctx, d.mailbox(), seq, wire, unixNow())
	d.signal()
	return true, err
}
