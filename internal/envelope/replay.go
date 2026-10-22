// Package envelope composes cryptography and recipient authority without effects.
package envelope

import (
	"github.com/temirlankasen/peerturn/internal/fault"
	"github.com/temirlankasen/peerturn/internal/protocol"
)

// MaxPendingSequences bounds both retained gaps and lookahead per directional channel.
// A future sequence beyond this window is backpressure, never permission to allocate.
const MaxPendingSequences = 64

// ReplayRecord is the durable fact that M1b must atomically persist with admission.
type ReplayRecord struct {
	// MessageID prevents retries from becoming independent admissions.
	MessageID string
	// Digest binds an ID to the original encapsulation, header and ciphertext.
	Digest string
	// SenderSeq detects replay under another message ID in the same channel.
	SenderSeq uint64
}

// ReplaySnapshot belongs to one exact directional session channel, not a mailbox.
// Callers must preserve records through the replay horizon and serialize updates.
type ReplaySnapshot struct {
	// NextSeq preserves a contiguous frontier instead of skipping unknown gaps.
	NextSeq uint64
	// Records retain prior decisions for the caller's durable replay horizon.
	Records []ReplayRecord
	// Pending retains at most MaxPendingSequences authenticated out-of-order packets.
	// M1b must serialize this snapshot and ciphertext storage in the same transaction.
	Pending []ReplayRecord
}

// CheckReplay is a pure decision; concurrent decisions on one snapshot are not commits.
// A gap is distinct from replay so callers retain only bounded, authenticated futures.
func CheckReplay(s ReplaySnapshot, r ReplayRecord) (bool, error) {
	if !protocol.ValidID(r.MessageID) || len(r.Digest) != 64 || !protocol.ValidID(r.Digest[:32]) ||
		!protocol.ValidID(r.Digest[32:]) || s.NextSeq == 0 || r.SenderSeq == 0 {
		return false, fault.New(fault.InvalidEncoding)
	}
	for _, old := range s.Records {
		if old.MessageID == r.MessageID {
			if old.Digest != r.Digest || old.SenderSeq != r.SenderSeq {
				return false, fault.New(fault.Conflict)
			}
			return true, nil
		}
		if old.SenderSeq == r.SenderSeq {
			return false, fault.New(fault.Replay)
		}
	}
	if r.SenderSeq < s.NextSeq {
		return false, fault.New(fault.Replay)
	}
	known := false
	for _, pending := range s.Pending {
		if pending.MessageID == r.MessageID {
			if pending.Digest != r.Digest || pending.SenderSeq != r.SenderSeq {
				return false, fault.New(fault.Conflict)
			}
			known = true
		} else if pending.SenderSeq == r.SenderSeq {
			return false, fault.New(fault.Replay)
		}
	}
	if r.SenderSeq > s.NextSeq {
		if r.SenderSeq-s.NextSeq > MaxPendingSequences || (!known && len(s.Pending) >= MaxPendingSequences) {
			return false, fault.New(fault.SequenceWindowExceeded)
		}
		return false, fault.New(fault.SequenceGap)
	}
	return false, nil
}
