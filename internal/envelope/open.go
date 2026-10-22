package envelope

import (
	"bytes"
	"crypto/ecdh"
	"crypto/ed25519"
	"slices"

	"github.com/temirlankasen/peerturn/internal/codec"
	peercrypto "github.com/temirlankasen/peerturn/internal/crypto"
	"github.com/temirlankasen/peerturn/internal/fault"
	"github.com/temirlankasen/peerturn/internal/grant"
	"github.com/temirlankasen/peerturn/internal/identity"
	"github.com/temirlankasen/peerturn/internal/protocol"
)

// OpenOptions are trusted caller state, after transport principal/permit binding.
// Pin and certificate epochs must come from current local state; never peer discovery.
type OpenOptions struct {
	// RecipientKey stays local and must match the verified recipient certificate.
	RecipientKey *ecdh.PrivateKey
	// RecipientChain is the receiving session's current locally selected chain.
	RecipientChain identity.Chain
	// RecipientPin prevents silently repinning the receiving identity.
	RecipientPin string
	// SenderChain comes from explicitly trusted session state rather than discovery.
	SenderChain identity.Chain
	// SenderPin rejects an unapproved root or root rotation.
	SenderPin string
	// SignedGrant is local recipient authority, never a peer-supplied scope label.
	SignedGrant string
	// CurrentGrantEpoch is the authoritative live revocation epoch.
	CurrentGrantEpoch uint64
	// Now makes clock trust and boundary expiry checks explicit to the caller.
	Now int64
	// ExpectedMailbox binds the authenticated transport destination before decryption.
	ExpectedMailbox string
	// ExpectedPermit binds the transport principal's checked route permit.
	ExpectedPermit string
	// AllowedKinds optionally narrows local product availability after authentication.
	// A peer cannot turn a signed broad grant into an unsupported runtime capability.
	AllowedKinds []protocol.Kind
	// Replay is a snapshot of the exact directional channel's committed decisions.
	Replay ReplaySnapshot
	// Conversation is the lookup by incoming conversation_id, including root attempts.
	// A nil value must mean this ID has no prior chain, never a skipped lookup.
	Conversation *Conversation
	// LookupConversation resolves authenticated chain metadata inside the caller's
	// custody transaction. When set, it replaces the static snapshot above.
	LookupConversation func(string) (*Conversation, error)
	// Budget supplies receiver-owned usage that sender identifiers cannot reset.
	Budget BudgetSnapshot
}

// Admission contains a proposed decision, not a storage receipt or model acceptance.
// M1b must commit its record and budgets atomically before any adapter may see it.
type Admission struct {
	// Message is returned only after all required authority checks succeed.
	Message protocol.MessagePayload
	// Record is proposed durable replay evidence, not an already committed write.
	Record ReplayRecord
	// Duplicate forbids a second native submission and allows prior receipt retrieval.
	Duplicate bool
	// WantsModel distinguishes a new model message from receipts and duplicates.
	WantsModel bool
	// Routing names the sole scope-dispatch destination; empty on duplicate or gap.
	Routing Routing
	// Gap is authenticated parking evidence only; it never authorizes dispatch.
	Gap *GapMetadata
	// Expired contains authenticated replay evidence only, never dispatch/body.
	Expired *GapMetadata
	// Rejected carries authenticated metadata for a custody refusal, never a body
	// or dispatch authority. It may consume only a sequence inside the replay window.
	Rejected *GapMetadata
}

// GapMetadata lets custody retain a future packet without returning its plaintext.
// The caller stores the original ciphertext, bounded by ReplaySnapshot.Pending.
type GapMetadata struct {
	Record    ReplayRecord
	ExpiresAt int64
	Sender    identity.Binding
	Recipient identity.Binding
	// Kind preserves the control lane while withholding all future plaintext.
	Kind protocol.Kind
}

// Open follows bounded routing, decrypt, signature/chain, audience, grant, replay and budgets.
// No plaintext or partially verified message is returned on a failure.
func Open(data []byte, o OpenOptions) (Admission, error) {
	return open(data, o, false)
}

// OpenForCustody additionally authenticates expired packets at the last second
// of their signed TTL. Only an Expired tombstone is returned in that case: never
// plaintext, routing or model authority. Live Open semantics remain unchanged.
func OpenForCustody(data []byte, o OpenOptions) (Admission, error) {
	return open(data, o, true)
}

func open(data []byte, o OpenOptions, custody bool) (Admission, error) {
	p, err := protocol.DecodePacket(data)
	if err != nil {
		return Admission{}, err
	}
	if p.Header.DstMailbox != o.ExpectedMailbox || p.Header.RoutePermitID != o.ExpectedPermit {
		return Admission{}, fault.New(fault.AudienceMismatch)
	}
	expired := custody && o.Now >= p.Header.ExpiresAt
	verificationTime := o.Now
	if expired {
		verificationTime = p.Header.ExpiresAt - 1
	}
	aad, err := p.Header.Canonical()
	if err != nil {
		return Admission{}, err
	}
	if len(p.Ciphertext) > protocol.MaxPlaintext+16 {
		return Admission{}, fault.New(fault.TooLarge)
	}
	compact, err := peercrypto.Open(o.RecipientKey, p.Enc, aad, p.Ciphertext)
	if err != nil {
		return Admission{}, err
	}
	sender, err := identity.VerifyChain(o.SenderChain, o.SenderPin, verificationTime)
	if err != nil {
		return Admission{}, err
	}
	raw, err := peercrypto.Verify(string(compact), identity.RootFingerprint(sender.SigningKey), peercrypto.MessageType, ed25519.PublicKey(sender.SigningKey))
	if err != nil {
		return Admission{}, err
	}
	var signed SignedMessage
	if checkErr := codec.Decode(raw, protocol.MaxPlaintext, &signed); checkErr != nil {
		return Admission{}, checkErr
	}
	if signed.Header != p.Header {
		return Admission{}, fault.New(fault.AudienceMismatch)
	}
	m := signed.Message
	if checkErr := m.Validate(); checkErr != nil {
		return Admission{}, checkErr
	}
	recipient, err := identity.VerifyChain(o.RecipientChain, o.RecipientPin, verificationTime)
	if err != nil {
		return Admission{}, err
	}
	if o.RecipientKey == nil || !bytes.Equal(o.RecipientKey.PublicKey().Bytes(), recipient.EncryptionKey) {
		return Admission{}, fault.New(fault.NeedsVerify)
	}
	if checkErr := checkAudience(p.Header, m, sender, recipient); checkErr != nil {
		return Admission{}, checkErr
	}
	g, err := grant.Verify(o.SignedGrant, recipient, sender, o.CurrentGrantEpoch, verificationTime)
	if err != nil {
		return Admission{}, err
	}
	if m.GrantID != g.GrantID || m.GrantHash != grant.Hash(o.SignedGrant) {
		return Admission{}, fault.New(fault.InvalidGrant)
	}
	if checkErr := grant.CompareEpoch(m.GrantEpoch, g.GrantEpoch); checkErr != nil {
		return Admission{}, checkErr
	}
	if m.CreatedAt > verificationTime {
		return Admission{}, fault.New(fault.NotYetValid)
	}
	if o.Now >= m.ExpiresAt && !expired {
		return Admission{}, fault.New(fault.Expired)
	}
	if m.CreatedAt < g.NotBefore || m.ExpiresAt > g.ExpiresAt {
		return Admission{}, fault.New(fault.InvalidGrant)
	}
	b, err := codec.Canonical(p)
	if err != nil {
		return Admission{}, err
	}
	r := ReplayRecord{MessageID: m.MessageID, Digest: peercrypto.Digest(b), SenderSeq: m.SenderSeq}
	metadata := &GapMetadata{Record: r, ExpiresAt: m.ExpiresAt, Sender: sender.Binding, Recipient: recipient.Binding, Kind: m.Kind}
	if len(o.AllowedKinds) > 0 {
		if !slices.Contains(o.AllowedKinds, m.Kind) {
			return custodyRefusal(custody, metadata, fault.New(fault.ScopeDenied))
		}
	}
	duplicate, err := CheckReplay(o.Replay, r)
	gap := fault.IsCode(err, fault.SequenceGap)
	if err != nil && !gap {
		if custody && fault.IsCode(err, fault.SequenceWindowExceeded) {
			return Admission{Rejected: metadata}, err
		}
		return Admission{}, err
	}
	if expired {
		if scopeErr := grant.CheckScope(g.Scope, string(m.Kind)); scopeErr != nil {
			return custodyRefusal(custody, metadata, scopeErr)
		}
		return Admission{Expired: metadata}, fault.New(fault.Expired)
	}
	conversation := o.Conversation
	if o.LookupConversation != nil && !duplicate && !gap {
		conversation, err = o.LookupConversation(m.ConversationID)
		if err != nil {
			return Admission{}, err
		}
	}
	route, routeErr := decideRouting(m, g, conversation, !duplicate && !gap)
	if routeErr != nil {
		return custodyRefusal(custody, metadata, routeErr)
	}
	if !duplicate {
		if checkErr := checkBudget(m, g.Limits, o.Budget, o.Now); checkErr != nil {
			return custodyRefusal(custody, metadata, checkErr)
		}
	}
	if gap {
		return Admission{Gap: metadata}, err
	}
	// Ask custody is not model admission. A qualified responder reserves and
	// charges its turn immediately before launch; rejected asks spend no turns.
	return Admission{Message: m, Record: r, Duplicate: duplicate, Routing: route, WantsModel: route == Live}, nil
}

func custodyRefusal(custody bool, metadata *GapMetadata, err error) (Admission, error) {
	if custody {
		return Admission{Rejected: metadata}, err
	}
	return Admission{}, err
}

func checkAudience(h protocol.RoutingHeader, m protocol.MessagePayload, s, r identity.SessionCertificate) error {
	if m.MessageID != h.MessageID || m.ExpiresAt != h.ExpiresAt || m.FromSession != s.SessionID || m.ToSession != r.SessionID {
		return fault.New(fault.AudienceMismatch)
	}
	if m.FromSessionEpoch != s.SessionEpoch || m.ToSessionEpoch != r.SessionEpoch || h.RecipientKeyEpoch != r.KeyEpoch {
		return fault.New(fault.EpochMismatch)
	}
	return nil
}
