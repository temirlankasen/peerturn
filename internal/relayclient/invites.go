package relayclient

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"

	"github.com/temirlankasen/peerturn/internal/codec"
	peercrypto "github.com/temirlankasen/peerturn/internal/crypto"
	"github.com/temirlankasen/peerturn/internal/invite"
	"github.com/temirlankasen/peerturn/internal/relay/auth"
)

// CreateInvite stores the encrypted descriptor under the routing key's signature.
func (c *Client) CreateInvite(ctx context.Context, input InviteRequest) error {
	_, err := c.CreateInviteWithReceipt(ctx, input)
	return err
}

// CreateInviteWithReceipt validates the publication ACK independently of
// discovery. Requiring own admission never accepts a legacy id-only ACK.
func (c *Client) CreateInviteWithReceipt(ctx context.Context, input InviteRequest) (InviteReceipt, error) {
	if !validInviteID(input.ID) || input.Packet.InviteID != input.ID || !auth.ValidRoutingKey(input.ProofKey) || !validExpiry(input.ExpiresAt) || len(input.Voucher) < 32 || len(input.Voucher) > 256 || !validInvitePacket(input.Packet) {
		return InviteReceipt{}, invalid()
	}
	var result struct {
		ID      string          `json:"id"`
		Mailbox json.RawMessage `json:"mailbox"`
	}
	if err := c.do(ctx, "POST", "/v1/invites/"+input.ID, nil, input, &result, 201); err != nil {
		return InviteReceipt{}, err
	}
	if result.ID != input.ID {
		return InviteReceipt{}, malformed()
	}
	receipt := InviteReceipt{ID: result.ID}
	// Preserve presence so explicit null/empty values cannot masquerade as an
	// omitted legacy field. The mailbox belongs to the routing signer, not proof_key.
	if result.Mailbox != nil {
		if err := codec.Decode(result.Mailbox, maxResponseBytes, &receipt.Mailbox); err != nil || receipt.Mailbox != peercrypto.Digest(c.key[ed25519.SeedSize:]) {
			return InviteReceipt{}, malformed()
		}
	}
	if input.RequireOwnMailbox && receipt.Mailbox == "" {
		return InviteReceipt{}, malformed()
	}
	return receipt, nil
}

// GetInvite fetches encrypted material without redeeming or interpreting a link.
func (c *Client) GetInvite(ctx context.Context, id string) (Invite, error) {
	if !validInviteID(id) {
		return Invite{}, invalid()
	}
	var result Invite
	if err := c.do(ctx, "GET", "/v1/invites/"+id, nil, nil, &result, 200); err != nil {
		return Invite{}, err
	}
	if result.ID != id || result.Packet.InviteID != id || !validInvitePacket(result.Packet) || !auth.ValidRoutingKey(result.ProofKey) || result.ExpiresAt <= 0 {
		return Invite{}, malformed()
	}
	if result.ClaimPacket != nil && (!validClaim(*result.ClaimPacket) || result.ClaimPacket.Packet.InviteID != id) {
		return Invite{}, malformed()
	}
	return result, nil
}

// ClaimInvite submits ciphertext and slot-possession proof, without local grant issuance.
func (c *Client) ClaimInvite(ctx context.Context, id string, packet invite.ClaimPacket) (ClaimReceipt, error) {
	if !validInviteID(id) || packet.Packet.InviteID != id || !validClaim(packet) {
		return ClaimReceipt{}, invalid()
	}
	var receipt ClaimReceipt
	if err := c.do(ctx, "POST", "/v1/invites/"+id+"/claims", nil, packet, &receipt, 200); err != nil {
		return ClaimReceipt{}, err
	}
	if receipt.Stage != "relay_stored" || !validInviteID(receipt.ClaimID) {
		return ClaimReceipt{}, malformed()
	}
	return receipt, nil
}

func validInviteID(id string) bool {
	if len(id) != 22 {
		return false
	}
	raw, err := base64.RawURLEncoding.Strict().DecodeString(id)
	return err == nil && len(raw) == 16 && base64.RawURLEncoding.EncodeToString(raw) == id
}

func validInvitePacket(packet invite.Packet) bool {
	data, err := codec.Canonical(packet)
	if err != nil {
		return false
	}
	_, err = invite.DecodePacket(data)
	return err == nil
}

func validClaim(packet invite.ClaimPacket) bool {
	data, err := codec.Canonical(packet)
	if err != nil {
		return false
	}
	_, err = invite.DecodeClaimPacket(data)
	return err == nil
}
