package store

import "testing"

func TestAskRechecksAuthorityBeforePeerFrameAfterStartupProbe(t *testing.T) {
	s, _, r := askFixture(t)
	reserveAsk(t, s, r)
	if err := s.AuthorizeAsk(t.Context(), r.RunID, testNow); err != nil {
		t.Fatal(err)
	}
	if err := s.AdvanceAsk(t.Context(), r.RunID, askEvidence(r, "responder_started"), []byte("child")); err != nil {
		t.Fatal(err)
	}
	if err := s.CheckAskAuthority(t.Context(), r.RunID, testNow); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(t.Context(), `UPDATE grants SET revoked=1 WHERE id='grant_one'`); err != nil {
		t.Fatal(err)
	}
	if err := s.CheckAskAuthority(t.Context(), r.RunID, testNow); err == nil {
		t.Fatal("revoked during trusted startup probe admitted peer input")
	}
}
