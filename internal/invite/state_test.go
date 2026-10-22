package invite

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"strings"
	"testing"

	"github.com/temirlankasen/peerturn/internal/fault"
)

func TestConsumeOneClaimAndLostAcknowledgment(t *testing.T) {
	id := base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat("i", 16)))
	initial, err := NewState(id, 600)
	if err != nil {
		t.Fatal(err)
	}
	claim := base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat("c", 16)))
	digest := strings.Repeat("a", 64)
	stored, result, err := initial.Consume(id, claim, digest, []byte("opaque-grant-response"), 100)
	if err != nil || !bytes.Equal(result, []byte("opaque-grant-response")) {
		t.Fatal("first consume failed", err)
	}
	result[0] = 'X'
	_, retry, err := stored.Consume(id, claim, digest, []byte("different-result"), 101)
	if err != nil || string(retry) != "opaque-grant-response" {
		t.Fatal("lost ACK retry changed stored result", err)
	}
	_, _, err = stored.Consume(id, claim, strings.Repeat("b", 64), []byte("other"), 101)
	if !fault.IsCode(err, fault.Conflict) {
		t.Fatal("same claim ID with different ciphertext was accepted")
	}
	if _, _, err := initial.Consume(id, claim, digest, []byte("independent snapshot"), 100); err != nil {
		t.Fatal("transition mutated its input", err)
	}
	if _, _, err := initial.Consume(claim, claim, digest, []byte("other slot"), 100); !fault.IsCode(err, fault.AudienceMismatch) {
		t.Fatal("claim consumed the wrong invitation slot", err)
	}
}

func TestFiftySerializedCompetingClaims(t *testing.T) {
	id := base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat("i", 16)))
	state, err := NewState(id, 600)
	if err != nil {
		t.Fatal(err)
	}
	for i := range 50 {
		claim := base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf("%016d", i)))
		next, _, err := state.Consume(id, claim, strings.Repeat("a", 64), []byte("opaque"), 100)
		if i == 0 {
			if err != nil {
				t.Fatal(err)
			}
			state = next
		} else if !fault.IsCode(err, fault.Conflict) {
			t.Fatalf("competing claim %d accepted", i)
		}
	}
}

func TestCompetingSnapshotsRequireAuthoritativeSerialization(t *testing.T) {
	id := base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat("i", 16)))
	initial, err := NewState(id, 600)
	if err != nil {
		t.Fatal(err)
	}
	claims := make([]string, 50)
	proposals := make([]State, 50)
	for i := range proposals {
		claims[i] = base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf("%016d", i)))
		// Every stale snapshot can propose a use; this function provides no lock or CAS.
		proposals[i], _, err = initial.Consume(id, claims[i], strings.Repeat("a", 64), []byte("opaque"), 100)
		if err != nil {
			t.Fatal("independent transition mutated another snapshot", err)
		}
	}
	// A future store must serialize publication of these proposals against current state.
	current := proposals[0]
	for _, claim := range claims[1:] {
		if _, _, consumeErr := current.Consume(id, claim, strings.Repeat("a", 64), []byte("opaque"), 100); !fault.IsCode(consumeErr, fault.Conflict) {
			t.Fatal("serialized state accepted another snapshot's claim", consumeErr)
		}
	}
}

func TestRevocationAndExpiryPreventNewUse(t *testing.T) {
	id := base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat("i", 16)))
	claim := base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat("c", 16)))
	state, err := NewState(id, 600)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, consumeErr := state.Revoke().Consume(id, claim, strings.Repeat("a", 64), []byte("opaque"), 100); !fault.IsCode(consumeErr, fault.Revoked) {
		t.Fatal("revoked invitation accepted a new claim")
	}
	if _, _, consumeErr := state.Consume(id, claim, strings.Repeat("a", 64), []byte("opaque"), 600); !fault.IsCode(consumeErr, fault.Expired) {
		t.Fatal("expired invitation accepted a new claim")
	}
	used, _, err := state.Consume(id, claim, strings.Repeat("a", 64), []byte("opaque"), 100)
	if err != nil {
		t.Fatal(err)
	}
	if _, result, err := used.Revoke().Consume(id, claim, strings.Repeat("a", 64), nil, 601); err != nil || string(result) != "opaque" {
		t.Fatal("invitation revoke incorrectly revoked an already issued result", err)
	}
}
