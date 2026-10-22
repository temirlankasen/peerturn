// Package relayclient implements the signed HTTPS ciphertext relay transport.
package relayclient

import (
	"context"
	"crypto/ed25519"
	"net/http"
	"time"

	"github.com/temirlankasen/peerturn/internal/invite"
	"github.com/temirlankasen/peerturn/internal/protocol"
)

// Options selects an explicit relay and a local routing signing key.
type Options struct {
	URL        string
	Key        ed25519.PrivateKey
	HTTPClient *http.Client
	// ExpectedRelayKey compares unsigned discovery metadata; TLS authenticates the relay.
	ExpectedRelayKey string
}

// Discovery describes unsigned public capabilities, not relay-key possession or E2E trust.
type Discovery struct {
	WireVersions []int  `json:"wire_versions"`
	RelayKey     string `json:"relay_key"`
	BackupEpoch  int64  `json:"backup_epoch"`
	Limits       Limits `json:"limits"`
}

// Limits are the relay's public custody and polling bounds.
type Limits struct {
	CiphertextBytes int   `json:"ciphertext_bytes"`
	BatchItems      int   `json:"batch_items"`
	MailboxItems    int   `json:"mailbox_items"`
	MailboxBytes    int64 `json:"mailbox_bytes"`
	LongPollSeconds int   `json:"long_poll_seconds"`
}

// MailboxRequest enrolls a reader through an admin or a scoped voucher.
type MailboxRequest struct {
	ID      string `json:"id"`
	ReadKey string `json:"read_key"`
	Voucher string `json:"voucher,omitempty"`
}

// Permit binds one routing writer to a recipient-owned epoch and expiry.
type Permit struct {
	WriterKey string `json:"writer_key"`
	Epoch     int64  `json:"epoch"`
	ExpiresAt int64  `json:"expires_at"`
}

// Receipt proves relay storage only, never receiver or model acceptance.
type Receipt struct {
	Stage     string `json:"stage"`
	Mailbox   string `json:"mailbox"`
	MessageID string `json:"message_id"`
	Seq       int64  `json:"seq"`
}

// FetchOptions starts from the caller's durable contiguous custody frontier.
type FetchOptions struct {
	After int64
	Limit int
	Wait  time.Duration
}

// Message carries either encrypted content or an expiry tombstone.
type Message struct {
	Seq       int64                     `json:"seq"`
	Packet    *protocol.EncryptedPacket `json:"packet,omitempty"`
	Tombstone bool                      `json:"tombstone,omitempty"`
}

// InviteRequest publishes ciphertext under explicit relay admin authority.
type InviteRequest struct {
	ID        string        `json:"-"`
	ProofKey  string        `json:"proof_key"`
	Packet    invite.Packet `json:"packet"`
	ExpiresAt int64         `json:"expires_at"`
	Voucher   string        `json:"voucher,omitempty"`
}

// Invite contains no plaintext voucher, policy or claim status for strangers.
type Invite struct {
	ID          string              `json:"id"`
	ProofKey    string              `json:"proof_key"`
	Packet      invite.Packet       `json:"packet"`
	ExpiresAt   int64               `json:"expires_at"`
	ClaimPacket *invite.ClaimPacket `json:"claim_packet,omitempty"`
}

// ClaimReceipt means the relay retained the encrypted claim, not a connection.
type ClaimReceipt struct {
	ClaimID string `json:"claim_id"`
	Stage   string `json:"stage"`
}

// Ticket allows one notification upgrade without granting ciphertext access.
type Ticket struct {
	Ticket    string `json:"ticket"`
	ExpiresAt int64  `json:"expires_at"`
}

// Event is an availability hint with the caller's current durable frontier.
type Event struct {
	Mailbox string `json:"mailbox"`
	HighSeq int64  `json:"high_seq"`
	After   int64  `json:"-"`
}

// NotifyOptions supplies durable state on every connection and notification.
// OnEvent fetches from After and commits locally before explicitly calling Ack.
type NotifyOptions struct {
	Frontier func(context.Context) (int64, error)
	OnEvent  func(context.Context, Event) error
}

// Error exposes a bounded relay transport category, never signed peer authority.
type Error struct {
	Status int
	Code   string
}

func (e *Error) Error() string { return "relay: " + e.Code }
