package invite

import "github.com/temirlankasen/peerturn/internal/fault"

// State is an immutable single-use decision snapshot, not a transactional store.
// A caller must atomically persist the returned state and result before issuing effects.
type State struct {
	id, claimID, digest string
	expiresAt           int64
	revoked             bool
	result              []byte
}

// NewState initializes a local decision for one public invitation ID.
func NewState(id string, expiresAt int64) (State, error) {
	if !encodedBytes(id, 16) || expiresAt <= 0 {
		return State{}, fault.New(fault.InvalidInvite)
	}
	return State{id: id, expiresAt: expiresAt}, nil
}

// Revoke rejects new joins without revoking a previously issued grant.
func (s State) Revoke() State {
	s.revoked = true
	return s
}

// Consume binds the slot ID and returns the prior result for an identical claim
// and ciphertext digest, including retries after invite expiry or revocation.
// Independent snapshots do not coordinate: authoritative concurrency control is M1b.
func (s State) Consume(inviteID, claimID, digest string, result []byte, now int64) (State, []byte, error) {
	if s.id != inviteID {
		return s, nil, fault.New(fault.AudienceMismatch)
	}
	if !encodedBytes(s.id, 16) || !encodedBytes(claimID, 16) || !validHash(digest) || len(result) > MaxPacketBytes {
		return s, nil, fault.New(fault.InvalidInvite)
	}
	if s.claimID != "" {
		if s.claimID == claimID && s.digest == digest {
			return s, append([]byte(nil), s.result...), nil
		}
		return s, nil, fault.New(fault.Conflict)
	}
	if s.revoked {
		return s, nil, fault.New(fault.Revoked)
	}
	if now >= s.expiresAt {
		return s, nil, fault.New(fault.Expired)
	}
	if len(result) == 0 || now < 0 {
		return s, nil, fault.New(fault.InvalidInvite)
	}
	s.claimID, s.digest, s.result = claimID, digest, append([]byte(nil), result...)
	return s, append([]byte(nil), s.result...), nil
}
