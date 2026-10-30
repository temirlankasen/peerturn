package store

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"path/filepath"
	"testing"
	"time"
)

const selfServiceZeroMailbox = "66687aadf862bd776c8fc18b8e9f8e20089714856ee233b3902a591d0d5f2925"

// These are synthetic public routing bytes. Signature authentication happens
// above this seam; store tests cannot establish possession of a private key.
func selfServiceKey(n byte) string {
	return base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{n}, 32))
}

// Only prepares an enrollment input. The derivation assertion uses the known
// SHA-256 vector above, independently of this convenience helper.
func selfServiceMailbox(t *testing.T, key string) Mailbox {
	t.Helper()
	raw, err := base64.RawURLEncoding.DecodeString(key)
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(raw)
	return Mailbox{ID: hex.EncodeToString(hash[:]), ReadKey: key}
}

func TestSelfServiceFixtureZeroMailboxMatchesIndependentVector(t *testing.T) {
	// Independently verified with Python hashlib.sha256(bytes(32)).hexdigest().
	const want = "66687aadf862bd776c8fc18b8e9f8e20089714856ee233b3902a591d0d5f2925"
	decoded, err := hex.DecodeString(selfServiceZeroMailbox)
	if err != nil || len(selfServiceZeroMailbox) != 64 || len(decoded) != 32 || selfServiceZeroMailbox != want {
		t.Fatalf("invalid zero-key mailbox vector: %q, %v", selfServiceZeroMailbox, err)
	}
	if got := selfServiceMailbox(t, selfServiceKey(0)); got.ID != want {
		t.Fatalf("fixture helper disagrees with independent SHA-256 vector: %q", got.ID)
	}
}

func selfServicePolicy() SelfServicePolicy {
	return SelfServicePolicy{
		MaxActiveInvites: 4, MaxInvitesPer24h: 20,
		MaxInvitesLifetime: 128, MaxInviters: 100,
	}
}

type selfServiceFixture struct {
	s      *Store
	path   string
	limits Limits
	now    time.Time
}

func newSelfServiceFixture(t *testing.T, limits Limits) *selfServiceFixture {
	t.Helper()
	f := &selfServiceFixture{
		path: filepath.Join(t.TempDir(), "relay.db"), limits: limits,
		now: time.Unix(1_800_000_000, 0).UTC(),
	}
	f.open(t)
	t.Cleanup(func() {
		if err := f.s.Close(); err != nil {
			t.Error(err)
		}
	})
	return f
}

func (f *selfServiceFixture) open(t *testing.T) {
	t.Helper()
	s, err := Open(f.path, f.limits)
	if err != nil {
		t.Fatal(err)
	}
	f.s = s
	f.s.SetClock(func() time.Time { return f.now })
}

func (f *selfServiceFixture) reopen(t *testing.T) {
	t.Helper()
	if err := f.s.Close(); err != nil {
		t.Fatal(err)
	}
	f.open(t)
}

func (f *selfServiceFixture) invite(id string) Invite {
	return Invite{
		ID: id, ProofKey: selfServiceKey(240), Packet: []byte("opaque-descriptor-" + id),
		ExpiresAt: f.now.Add(time.Minute), Voucher: "synthetic-voucher-" + id,
	}
}

func (f *selfServiceFixture) create(t *testing.T, key, id string, policy SelfServicePolicy) (Mailbox, Invite) {
	t.Helper()
	invite := f.invite(id)
	mailbox, err := f.s.CreateOwnInvite(t.Context(), key, invite, policy)
	if err != nil {
		t.Fatalf("self-service creation %s: %v", id, err)
	}
	return mailbox, invite
}

func selfServiceRequireNoCreation(t *testing.T, mailbox Mailbox, err error, status int) {
	t.Helper()
	requireStatus(t, err, status)
	if mailbox != (Mailbox{}) {
		t.Fatalf("rejected admission exposed a mailbox: %+v", mailbox)
	}
}

func (f *selfServiceFixture) requireDescriptor(t *testing.T, want Invite) {
	t.Helper()
	got, err := f.s.GetInvite(t.Context(), want.ID, f.now)
	if err != nil || got.ID != want.ID || got.ProofKey != want.ProofKey ||
		!got.ExpiresAt.Equal(want.ExpiresAt) || !bytes.Equal(got.Packet, want.Packet) || got.Voucher != "" {
		t.Fatalf("descriptor changed or voucher exposed: %+v, %v", got, err)
	}
}

func (f *selfServiceFixture) claim(t *testing.T, invite Invite, key string) Claim {
	t.Helper()
	claim := Claim{ID: "claim-" + invite.ID, ReaderKey: key, Packet: []byte("opaque-claim-" + invite.ID)}
	got, err := f.s.ClaimInvite(t.Context(), invite.ID, claim, f.now)
	if err != nil || got.Voucher != "" {
		t.Fatalf("claim: %+v, %v", got, err)
	}
	return claim
}
