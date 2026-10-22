// Package store provides the relay's durable ciphertext custody and routing ACLs.
package store

import "time"

// Limits bound both stored ciphertext and retained replay metadata.
type Limits struct {
	GlobalBytes  int64
	MailboxItems int
	MailboxBytes int64
	// MetadataItems caps permanent/control rows and, independently, replay nonces.
	MetadataItems int
}

// Mailbox binds a single routing public key to content access.
type Mailbox struct {
	ID      string
	ReadKey string
}

// Permit authorizes a routing writer without revealing an E2E grant.
type Permit struct {
	ID        string
	WriterKey string
	Epoch     int64
	ExpiresAt time.Time
	Revoked   bool
}

// Invite contains an opaque descriptor and its public proof verification key.
type Invite struct {
	ID       string
	ProofKey string
	Packet   []byte
	// ClaimPacket exposes only the committed encrypted winner for inviter-local verification.
	ClaimPacket []byte
	ClaimReader string
	ExpiresAt   time.Time
	Voucher     string
}

// Claim records the winning ciphertext and enrollment routing key.
type Claim struct {
	ID        string
	ReaderKey string
	Packet    []byte
	Voucher   string
}

// Message contains only routing metadata and a canonical encrypted packet.
type Message struct {
	MailboxID   string
	PermitID    string
	WriterKey   string
	ID          string
	PermitEpoch int64
	Packet      []byte
	ExpiresAt   time.Time
	Seq         int64
	Tombstone   bool
}

// Receipt is issued only after durable transaction commit.
type Receipt struct {
	Seq       int64
	Duplicate bool
}

// Ticket grants one notification stream upgrade, never content access.
type Ticket struct {
	ID          string
	MailboxID   string
	ReaderKey   string
	PermitID    string
	WriterKey   string
	PermitEpoch int64
	ExpiresAt   time.Time
}

// Error exposes a stable category without SQL, paths, or input values.
type Error struct {
	Code   string
	Status int
}

func (e *Error) Error() string { return e.Code }

func invalid() error     { return &Error{Code: "invalid", Status: 400} }
func forbidden() error   { return &Error{Code: "forbidden", Status: 403} }
func conflict() error    { return &Error{Code: "conflict", Status: 409} }
func gone() error        { return &Error{Code: "gone", Status: 410} }
func quota() error       { return &Error{Code: "quota", Status: 429} }
func unavailable() error { return &Error{Code: "storage_unavailable", Status: 503} }

func validID(value string) bool { return len(value) > 0 && len(value) <= 256 }

const maxPacketBytes = 192 << 10

// Message retention bounds limit churn and keep deduplication finite after ACK.
const (
	MinMessageTTL = time.Minute
	MaxMessageTTL = 7 * 24 * time.Hour
	DedupGrace    = time.Hour
)

func validPacket(packet []byte) bool { return len(packet) > 0 && len(packet) <= maxPacketBytes }
