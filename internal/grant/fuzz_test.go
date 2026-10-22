package grant

import (
	"testing"

	"github.com/temirlankasen/peerturn/internal/codec"
)

func FuzzGrantDecode(f *testing.F) {
	g, _, _, _ := grantFixture(f)
	valid, err := codec.Canonical(g)
	if err != nil {
		f.Fatal(err)
	}
	f.Add(valid)
	f.Add([]byte(`{"wire_version":1,"scope":"coauthor","scope":"ask"}`))
	f.Add([]byte(`{"issuer":null,"subject":null}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		decoded, decodeErr := Decode(data)
		if decodeErr != nil {
			return
		}
		canonical, canonicalErr := codec.Canonical(decoded)
		if canonicalErr != nil {
			t.Fatal(canonicalErr)
		}
		if _, roundTripErr := Decode(canonical); roundTripErr != nil {
			t.Fatalf("accepted payload cannot round trip: %v", roundTripErr)
		}
	})
}

func FuzzGrantVerify(f *testing.F) {
	g, issuer, subject, key := grantFixture(f)
	valid, err := Issue(g, key)
	if err != nil {
		f.Fatal(err)
	}
	f.Add(valid)
	f.Add("e30.e30.AA")
	f.Fuzz(func(t *testing.T, compact string) {
		if verified, verifyErr := Verify(compact, issuer, subject, g.GrantEpoch, fixtureNow); verifyErr == nil &&
			verified.GrantID != g.GrantID {
			t.Fatal("unissued grant accepted")
		}
	})
}
