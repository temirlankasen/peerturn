package identity

import (
	"testing"

	"github.com/temirlankasen/peerturn/internal/codec"
)

func FuzzCertificateDecoders(f *testing.F) {
	_, cert, _ := identityFixture(f)
	valid, err := codec.Canonical(cert)
	if err != nil {
		f.Fatal(err)
	}
	f.Add(valid)
	device := DeviceCertificate{Version: 1, IdentityID: cert.IdentityID, DeviceID: cert.DeviceID,
		DeviceEpoch: cert.DeviceEpoch, SigningKey: cert.SigningKey, NotBefore: cert.NotBefore, ExpiresAt: cert.ExpiresAt}
	devicePayload, err := codec.Canonical(device)
	if err != nil {
		f.Fatal(err)
	}
	f.Add(devicePayload)
	newPublic, _ := newKey(f)
	rotation := RotationStatement{Version: 1, OldIdentityID: cert.IdentityID, NewIdentityID: RootFingerprint(newPublic),
		NewRootPublicKey: newPublic, NotBefore: cert.NotBefore, ExpiresAt: cert.ExpiresAt}
	rotationPayload, err := codec.Canonical(rotation)
	if err != nil {
		f.Fatal(err)
	}
	f.Add(rotationPayload)
	f.Add([]byte(`{"wire_version":1,"wire_version":1}`))
	f.Add([]byte(`{"signing_key":null,"encryption_key":[]}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		fuzzDecode(t, data, DecodeSession)
		fuzzDecode(t, data, DecodeDevice)
		fuzzDecode(t, data, DecodeRotation)
	})
}

func fuzzDecode[T any](t *testing.T, data []byte, decode func([]byte) (T, error)) {
	t.Helper()
	value, err := decode(data)
	if err != nil {
		return
	}
	canonical, err := codec.Canonical(value)
	if err != nil {
		t.Fatal(err)
	}
	if _, decodeErr := decode(canonical); decodeErr != nil {
		t.Fatalf("accepted payload cannot round trip: %v", decodeErr)
	}
}

func FuzzVerifyChain(f *testing.F) {
	chain, cert, _ := identityFixture(f)
	valid, err := codec.Canonical(chain)
	if err != nil {
		f.Fatal(err)
	}
	f.Add(valid)
	f.Add([]byte(`{"root_public_key":"","device":"","session":""}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		var candidate Chain
		if codec.Decode(data, 32*1024, &candidate) != nil {
			return
		}
		if verified, verifyErr := VerifyChain(candidate, cert.IdentityID, fixtureNow); verifyErr == nil &&
			!EqualBinding(verified.Binding, cert.Binding) {
			t.Fatal("unissued session accepted")
		}
	})
}
