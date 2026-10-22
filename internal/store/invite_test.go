package store

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"sync"
	"testing"

	"github.com/temirlankasen/peerturn/internal/fault"
)

func TestInviteConcurrentWinnerAndRetry(t *testing.T) {
	s := testStore(t)
	const id = "AAAAAAAAAAAAAAAAAAAAAA"
	if err := s.CreateInvite(t.Context(), id, 100); err != nil {
		t.Fatal(err)
	}
	var seq int
	var name, path string
	if err := s.db.QueryRowContext(t.Context(), `PRAGMA database_list`).Scan(&seq, &name, &path); err != nil {
		t.Fatal(err)
	}
	other, err := Open(t.Context(), path, Options{Sealer: s.sealer, Clock: s.clock})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if closeErr := other.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	})
	var wg sync.WaitGroup
	results := make(chan string, 64)
	claims := map[string]string{}
	for i := range 64 {
		claims[base64.RawURLEncoding.EncodeToString(fmt.Appendf(nil, "%016d", i))] = fmt.Sprintf("%064d", i)
	}
	i := 0
	for claim, digest := range claims {
		handle := []*Store{s, other}[i%2]
		i++
		wg.Go(func() {
			result, consumeErr := handle.ConsumeInvite(t.Context(), id, claim, digest, []byte(claim), 10)
			if consumeErr == nil {
				results <- string(result)
			} else if !fault.IsCode(consumeErr, fault.Conflict) {
				t.Errorf("claim: %v", consumeErr)
			}
		})
	}
	wg.Wait()
	close(results)
	var winner string
	for result := range results {
		if winner != "" {
			t.Fatal("multiple winners")
		}
		winner = result
	}
	if winner == "" {
		t.Fatal("no winner")
	}
	got, err := s.ConsumeInvite(t.Context(), id, winner, claims[winner], []byte("changed result"), 200)
	if err != nil || !bytes.Equal(got, []byte(winner)) {
		t.Fatalf("lost reply retry: %q %v", got, err)
	}
}

func TestInviteAuditIdentifiesEachTransition(t *testing.T) {
	s := testStore(t)
	const id = "AAAAAAAAAAAAAAAAAAAAAA"
	for range 2 {
		if err := s.CreateInvite(t.Context(), id, testNow+300); err != nil {
			t.Fatal(err)
		}
	}
	const claim = "AAAAAAAAAAAAAAAAAAAAAQ"
	const digest = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	if _, err := s.ConsumeInvite(t.Context(), id, claim, digest, []byte("private-result"), testNow); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := s.RevokeInvite(t.Context(), id); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.ConsumeInvite(t.Context(), id, claim, digest, []byte("different-result"), testNow+400); err != nil {
		t.Fatal(err)
	}
	for _, action := range []string{"invite_create", "invite_consume", "invite_revoke"} {
		var count int
		if err := s.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM audit WHERE action=? AND message_id=? AND at=?`, action, id, testNow).Scan(&count); err != nil || count != 1 {
			t.Fatalf("missing/idempotent audit %s: count=%d err=%v", action, count, err)
		}
	}
}
