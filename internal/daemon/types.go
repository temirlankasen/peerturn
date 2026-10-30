// Package daemon connects explicit sessions to durable encrypted custody and
// qualified native adapters, keeping storage separate from model receipt.
package daemon

import (
	"crypto/ed25519"
	"github.com/temirlankasen/peerturn/internal/adapters/codex"
	"net/http"

	"github.com/temirlankasen/peerturn/internal/identity"
	"github.com/temirlankasen/peerturn/internal/invite"
	"github.com/temirlankasen/peerturn/internal/keystore"
	"github.com/temirlankasen/peerturn/internal/localipc"
	"github.com/temirlankasen/peerturn/internal/protocol"
	"github.com/temirlankasen/peerturn/internal/runtimepolicy"
)

// InitOptions requires an explicit storage policy and an owner-selected test label.
type InitOptions struct {
	StateDir, RelayURL, SessionLabel string
	ConfigPath                       string
	FilePermissions                  bool
	KeyStore                         keystore.Backend
	PinnedRelayKey                   string
	// RoutingKey is an optional locally provisioned routing identity, never an IPC argument.
	RoutingKey    ed25519.PrivateKey
	CodexThreadID string
	Native        NativeRuntime
}

// Options selects local storage and IPC. RuntimeDir overrides are for isolated tests;
// normal CLI operation always uses the single per-uid runtime directory.
type Options struct {
	StateDir, RuntimeDir string
	ConfigPath           string
	HTTPClient           *http.Client
	Native               NativeRuntime
	KeyStore             keystore.Backend
}

// Capabilities distinguishes encrypted custody from native runtime integration.
type Capabilities struct {
	E2E                      bool     `json:"e2e"`
	AttachExisting           bool     `json:"attach_existing"`
	WakeIdle                 bool     `json:"wake_idle"`
	BoundaryDelivery         bool     `json:"boundary_delivery"`
	ForkResponder            bool     `json:"fork_responder"`
	InformBoundary           bool     `json:"inform_boundary"`
	AuthoritySecretsIsolated bool     `json:"authority_secrets_isolated"`
	ReceiptStages            []string `json:"receipt_stages"`
}

// Status distinguishes preview bindings, native probes and observed hook activation.
type Status struct {
	ProfileID        string                     `json:"profile_id,omitempty"`
	RuntimePolicy    *runtimepolicy.Report      `json:"runtime_policy,omitempty"`
	OwnerInvitations []localipc.OwnerInvitation `json:"owner_invitations,omitempty"`
	RuntimeVersion   string                     `json:"runtime_version,omitempty"`
	Provider         string                     `json:"provider,omitempty"`
	State            string                     `json:"state"`
	Binding          string                     `json:"binding"`
	Session          string                     `json:"session"`
	Fingerprint      string                     `json:"fingerprint"`
	RoutingKey       string                     `json:"routing_key"`
	SupportedScopes  []string                   `json:"supported_scopes"`
	Capabilities     Capabilities               `json:"capabilities"`
	LastVerifiedAt   int64                      `json:"last_verified_at"`
	LastError        string                     `json:"last_error,omitempty"`
	Warning          string                     `json:"warning"`
	Notifications    string                     `json:"notifications"`
	NativeThread     string                     `json:"native_thread,omitempty"`
	AdapterReason    string                     `json:"adapter_reason,omitempty"`
	ResponderReason  string                     `json:"responder_reason,omitempty"`
	Responder        ResponderPreflight         `json:"responder"`
	InformPending    int                        `json:"inform_pending"`
	InformBoundary   string                     `json:"inform_boundary,omitempty"`
	OnboardingErrors []string                   `json:"onboarding_errors,omitempty"`
}

// Peer is the exact attached remote session, never a discovery result.
type Peer struct {
	ID          string `json:"id"`
	Fingerprint string `json:"fingerprint"`
	State       string `json:"state"`
	Scope       string `json:"scope"`
	ExpiresAt   int64  `json:"expires_at"`
}

// InboxPage exposes continuation rather than silently hiding retained messages.
type InboxPage struct {
	Messages   []InboxMessage `json:"messages"`
	NextCursor string         `json:"next_cursor,omitempty"`
}

// InboxMessage is literal local custody; routing is not native input evidence.
type InboxMessage struct {
	MessageID string `json:"message_id"`
	Peer      string `json:"peer"`
	BodyUTF8  string `json:"body_utf8"`
	Routing   string `json:"routing"`
	State     string `json:"state"`
	Stage     string `json:"stage"`
}

type peerState struct {
	Chain                    identity.Chain `json:"chain"`
	RoutingKey               []byte         `json:"routing_key"`
	Incoming                 string         `json:"incoming"`
	Outgoing                 string         `json:"outgoing"`
	Revoked                  bool           `json:"revoked"`
	IncomingRevoked          bool           `json:"incoming_revoked"`
	RevokeRoutePending       bool           `json:"revoke_route_pending"`
	SequenceBlocked          bool           `json:"sequence_blocked"`
	NotificationsUnavailable bool           `json:"notifications_unavailable"`
	HelloSent                bool           `json:"hello_sent"`
	HelloReceived            bool           `json:"hello_received"`
}

type issuedInvite struct {
	// Runtime scheduling and ACK confirmation never enter the sealed journal.
	publication  invitePublication
	Failure      string                    `json:"failure,omitempty"`
	Terminal     bool                      `json:"terminal"`
	URL          string                    `json:"url"`
	Descriptor   invite.Descriptor         `json:"descriptor"`
	Packet       invite.Packet             `json:"packet"`
	Claim        *invite.Claim             `json:"claim,omitempty"`
	ClaimDigest  string                    `json:"claim_digest,omitempty"`
	ForwardGrant string                    `json:"forward_grant,omitempty"`
	Response     *protocol.EncryptedPacket `json:"response,omitempty"`
	Sent         bool                      `json:"sent"`
}

type joiningInvite struct {
	URLDigest      string                    `json:"url_digest,omitempty"`
	Failure        string                    `json:"failure,omitempty"`
	Terminal       bool                      `json:"terminal"`
	URL            string                    `json:"url"`
	Descriptor     invite.Descriptor         `json:"descriptor"`
	Claim          invite.Claim              `json:"claim"`
	Packet         invite.ClaimPacket        `json:"packet"`
	Claimed        bool                      `json:"claimed"`
	Enrolled       bool                      `json:"enrolled"`
	Complete       bool                      `json:"complete"`
	ResponseID     string                    `json:"response_id,omitempty"`
	ResponseDigest string                    `json:"response_digest,omitempty"`
	Ack            *protocol.EncryptedPacket `json:"ack,omitempty"`
	AckSent        bool                      `json:"ack_sent"`
}

// The sealed journal holds keys and onboarding decisions, not a second message DB.
// Message bodies and retry ciphertext live exclusively in M1b's queues.
type clientState struct {
	AskBudget       *askBudgetState           `json:"ask_budget,omitempty"`
	ClaudeProject   string                    `json:"claude_project,omitempty"`
	NativeSessionID string                    `json:"native_session_id,omitempty"`
	Version         int                       `json:"version"`
	RelayURL        string                    `json:"relay_url"`
	PinnedRelayKey  string                    `json:"pinned_relay_key,omitempty"`
	Label           string                    `json:"label"`
	RootSeed        []byte                    `json:"root_seed"`
	DeviceSeed      []byte                    `json:"device_seed"`
	SigningSeed     []byte                    `json:"signing_seed"`
	EncryptionKey   []byte                    `json:"encryption_key"`
	RoutingSeed     []byte                    `json:"routing_seed"`
	Chain           identity.Chain            `json:"chain"`
	Peers           map[string]*peerState     `json:"peers"`
	Issued          map[string]*issuedInvite  `json:"issued"`
	Joining         map[string]*joiningInvite `json:"joining"`
	Sequences       map[string]uint64         `json:"sequences"`
	Pending         map[string]outboundOrder  `json:"pending"`
	Enrolled        bool                      `json:"enrolled"`
	// Reserved retains a sequence and its exact packet until queue commit succeeds.
	Reserved *reservedPacket `json:"reserved,omitempty"`
	Codex    *codex.Binding  `json:"codex,omitempty"`
}

type reservedPacket struct {
	PeerID          string                              `json:"peer_id"`
	Message         protocol.MessagePayload             `json:"message"`
	Packet          protocol.EncryptedPacket            `json:"packet"`
	Inbound         *protocol.EncryptedPacket           `json:"inbound,omitempty"`
	MailboxSeq      uint64                              `json:"mailbox_seq,omitempty"`
	RevokeGrant     string                              `json:"revoke_grant,omitempty"`
	Revocations     map[string]protocol.EncryptedPacket `json:"revocations,omitempty"`
	NoNotifications bool                                `json:"no_notifications"`
	Orders          map[string]outboundOrder            `json:"orders,omitempty"`
	NativeBatch     []reservedPacket                    `json:"native_batch,omitempty"`
}

type outboundOrder struct {
	PeerID    string `json:"peer_id"`
	SenderSeq uint64 `json:"sender_seq"`
}
