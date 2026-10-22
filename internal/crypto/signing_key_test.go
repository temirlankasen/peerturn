package crypto

import (
	"bytes"
	"encoding/hex"
	"testing"

	"github.com/temirlankasen/peerturn/internal/fault"
)

func TestSigningKeyRejectsAllEightSmallOrderPoints(t *testing.T) {
	for _, encoded := range []string{
		"0000000000000000000000000000000000000000000000000000000000000000",
		"0000000000000000000000000000000000000000000000000000000000000080",
		"0100000000000000000000000000000000000000000000000000000000000000",
		"ecffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff7f",
		"26e8958fc2b227b045c3f489f2ef98f0d5dfac05d3c63339b13802886d53fc05",
		"26e8958fc2b227b045c3f489f2ef98f0d5dfac05d3c63339b13802886d53fc85",
		"c7176a703d4dd84fba3c0b760d10670f2a2053fa2c39ccc64ec7fd7792ac037a",
		"c7176a703d4dd84fba3c0b760d10670f2a2053fa2c39ccc64ec7fd7792ac03fa",
		// These two x=0 sign aliases are noncanonical rather than new points.
		"0100000000000000000000000000000000000000000000000000000000000080",
		"ecffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff",
	} {
		raw := fromHex(t, encoded)
		if ValidSigningKey(raw) {
			t.Errorf("accepted torsion encoding %s", encoded)
		}
		if _, err := Verify("", "k", MessageType, raw); !fault.IsCode(err, fault.InvalidSignature) {
			t.Errorf("torsion key reached JWS parse %s: %v", encoded, err)
		}
	}
}

func TestSigningKeyRequiresCanonicalY(t *testing.T) {
	for first := 0xed; first <= 0xff; first++ {
		for _, sign := range []byte{0x7f, 0xff} {
			raw := bytes.Repeat([]byte{0xff}, 32)
			raw[0], raw[31] = byte(first), sign
			if ValidSigningKey(raw) {
				t.Errorf("accepted y >= p: %s", hex.EncodeToString(raw))
			}
		}
	}
	for _, raw := range [][]byte{nil, make([]byte, 31), make([]byte, 33)} {
		if ValidSigningKey(raw) {
			t.Errorf("accepted key length %d", len(raw))
		}
	}
	for range 32 {
		public := signingPublic(t, signingKey(t))
		if !ValidSigningKey(public) {
			t.Fatal("generated public key rejected")
		}
	}
}
