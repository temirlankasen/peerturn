package store

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRelaySchemaV2RegistryRowsChargeMetadataAcrossReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry-budget.db")
	limits := Limits{MetadataItems: 2}
	original := migrationOpenWithLimits(t, path, limits)
	for _, entry := range []struct {
		key     string
		revoked bool
	}{
		{strings.Repeat("A", 43), false},
		{strings.Repeat("B", 43), true},
	} {
		migrationExec(t, original.db, `INSERT INTO relay_inviters(routing_key,revoked) VALUES(?,?)`, entry.key, entry.revoked)
	}
	if err := original.Close(); err != nil {
		t.Fatal(err)
	}
	s := migrationOpenWithLimits(t, path, limits)
	now := time.Unix(1_800_000_000, 0).UTC()
	s.SetClock(func() time.Time { return now })
	requireStatus(t, s.CreateMailbox(t.Context(), Mailbox{ID: "over-budget", ReadKey: "reader"}, ""), 429)
	migrationInt(t, s.db, `SELECT COUNT(*) FROM mailboxes`, 0)
	migrationInt(t, s.db, `SELECT COUNT(*) FROM relay_inviters`, 2)
	// Registry rows spend custody metadata, including blocked entries, while
	// the existing independent recovery nonce pool remains usable.
	if err := s.UseControlNonce(t.Context(), "reader", "recovery", now.Add(time.Minute), now); err != nil {
		t.Fatalf("registry pressure blocked control replay capacity: %v", err)
	}
}
