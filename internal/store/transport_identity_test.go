package store

import (
	"strings"
	"testing"

	"github.com/temirlankasen/peerturn/internal/fault"
	"github.com/temirlankasen/peerturn/internal/identity"
)

func TestInstallSessionAllowsExplicitNativeBindingWithoutGrant(t *testing.T) {
	s := testStore(t)
	p := newTestParty(t, "local_owner")
	for range 2 {
		if err := s.InstallSession(t.Context(), p.chain, p.cert.IdentityID, testNow); err != nil {
			t.Fatal("explicit session installation failed", err)
		}
	}
	if err := s.BindSession(t.Context(), p.cert.Binding, []byte("owner-selected-native-context")); err != nil {
		t.Fatal("installed session could not bind its native context", err)
	}
	var sessions, grants, bindings int
	if err := s.db.QueryRowContext(t.Context(), `SELECT (SELECT COUNT(*) FROM sessions),(SELECT COUNT(*) FROM grants),(SELECT COUNT(*) FROM bindings)`).Scan(&sessions, &grants, &bindings); err != nil || sessions != 1 || grants != 0 || bindings != 1 {
		t.Fatalf("explicit installation changed authority: sessions=%d grants=%d bindings=%d %v", sessions, grants, bindings, err)
	}
}

func TestInstallSessionRejectsInvalidChainBeforePersistence(t *testing.T) {
	s := testStore(t)
	p := newTestParty(t, "untrusted")
	for _, tc := range []struct {
		name  string
		chain identity.Chain
		pin   string
		now   int64
		code  fault.Code
	}{
		{"wrong_pin", p.chain, strings.Repeat("a", 64), testNow, fault.NeedsVerify},
		{"expired", p.chain, p.cert.IdentityID, p.cert.ExpiresAt, fault.Expired},
		{"future", p.chain, p.cert.IdentityID, p.cert.NotBefore - 1, fault.NotYetValid},
		{"empty", identity.Chain{}, p.cert.IdentityID, testNow, fault.InvalidCertificate},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := s.InstallSession(t.Context(), tc.chain, tc.pin, tc.now); !fault.IsCode(err, tc.code) {
				t.Fatal("untrusted session accepted", err)
			}
		})
	}
	var count int
	if err := s.db.QueryRowContext(t.Context(), `SELECT (SELECT COUNT(*) FROM identities)+(SELECT COUNT(*) FROM devices)+(SELECT COUNT(*) FROM sessions)+(SELECT COUNT(*) FROM bindings)`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("failed session authentication wrote state: %d %v", count, err)
	}
}
