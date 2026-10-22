package identity

import (
	"bytes"
	"crypto/ed25519"
	"encoding/hex"
	"testing"

	"github.com/temirlankasen/peerturn/internal/codec"
	peercrypto "github.com/temirlankasen/peerturn/internal/crypto"
	"github.com/temirlankasen/peerturn/internal/fault"
)

func TestAuthorityLifetimeBoundaries(t *testing.T) {
	_, session, deviceKey := identityFixture(t)
	rootPublic, rootKey := newKey(t)
	device := DeviceCertificate{Version: 1, IdentityID: RootFingerprint(rootPublic), DeviceID: "device",
		DeviceEpoch: 1, SigningKey: deviceKey[32:], NotBefore: fixtureNow}
	newPublic, newPrivate := newKey(t)
	rotation := RotationStatement{Version: 1, OldIdentityID: device.IdentityID,
		NewIdentityID: RootFingerprint(newPublic), NewRootPublicKey: newPublic, NotBefore: fixtureNow}
	for _, delta := range []int64{-1, 0, 1} {
		device.ExpiresAt = device.NotBefore + 30*24*60*60 + delta
		_, err := IssueDevice(device, rootKey)
		assertLifetimeDecision(t, "device", delta, err)
		payload, canonicalErr := codec.Canonical(device)
		if canonicalErr != nil {
			t.Fatal(canonicalErr)
		}
		_, err = DecodeDevice(payload)
		assertLifetimeDecision(t, "decoded device", delta, err)

		session.ExpiresAt = session.NotBefore + 24*60*60 + 5*60 + delta
		_, err = IssueSession(session, deviceKey)
		assertLifetimeDecision(t, "session", delta, err)
		payload, canonicalErr = codec.Canonical(session)
		if canonicalErr != nil {
			t.Fatal(canonicalErr)
		}
		_, err = DecodeSession(payload)
		assertLifetimeDecision(t, "decoded session", delta, err)

		rotation.ExpiresAt = rotation.NotBefore + 3*24*60*60 + delta
		_, err = IssueRotation(rotation, rootKey, newPrivate)
		assertLifetimeDecision(t, "rotation", delta, err)
		payload, canonicalErr = codec.Canonical(rotation)
		if canonicalErr != nil {
			t.Fatal(canonicalErr)
		}
		_, err = DecodeRotation(payload)
		assertLifetimeDecision(t, "decoded rotation", delta, err)
	}
}

func assertLifetimeDecision(t *testing.T, object string, delta int64, err error) {
	t.Helper()
	if delta <= 0 && err != nil || delta > 0 && !fault.IsCode(err, fault.InvalidCertificate) {
		t.Errorf("%s lifetime cap %+d: %v", object, delta, err)
	}
}

func TestIdentityRejectsWeakSigningKeys(t *testing.T) {
	chain, session, _ := identityFixture(t)
	rootPublic, _ := newKey(t)
	for _, raw := range weakSigningKeys(t) {
		t.Run(hex.EncodeToString(raw), func(t *testing.T) {
			bad := session
			bad.SigningKey = raw
			if err := ValidateBinding(bad.Binding); !fault.IsCode(err, fault.InvalidCertificate) {
				t.Errorf("binding accepted weak key: %v", err)
			}
			payload, err := codec.Canonical(bad)
			if err != nil {
				t.Fatal(err)
			}
			if _, decodeErr := DecodeSession(payload); !fault.IsCode(decodeErr, fault.InvalidCertificate) {
				t.Errorf("session accepted weak key: %v", decodeErr)
			}
			device := DeviceCertificate{Version: 1, IdentityID: session.IdentityID, DeviceID: session.DeviceID,
				DeviceEpoch: 1, SigningKey: raw, NotBefore: fixtureNow - 10, ExpiresAt: fixtureNow + 7200}
			payload, err = codec.Canonical(device)
			if err != nil {
				t.Fatal(err)
			}
			if _, decodeErr := DecodeDevice(payload); !fault.IsCode(decodeErr, fault.InvalidCertificate) {
				t.Errorf("device accepted weak key: %v", decodeErr)
			}
			rotation := RotationStatement{Version: 1, OldIdentityID: RootFingerprint(rootPublic),
				NewIdentityID: RootFingerprint(raw), NewRootPublicKey: raw, NotBefore: fixtureNow - 1, ExpiresAt: fixtureNow + 100}
			payload, err = codec.Canonical(rotation)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := DecodeRotation(payload); !fault.IsCode(err, fault.InvalidCertificate) {
				t.Errorf("rotation accepted weak replacement: %v", err)
			}
			badChain := chain
			badChain.RootPublicKey = raw
			if _, err := VerifyChain(badChain, RootFingerprint(raw), fixtureNow); !fault.IsCode(err, fault.InvalidCertificate) {
				t.Errorf("chain weak root code: %v", err)
			}
			badRotation := Rotation{OldRootPublicKey: raw}
			if _, err := VerifyRotation(badRotation, RootFingerprint(raw), fixtureNow); !fault.IsCode(err, fault.InvalidCertificate) {
				t.Errorf("rotation weak old root code: %v", err)
			}
		})
	}
}

func TestVerifyChainRejectsSignedExcessiveLifetime(t *testing.T) {
	rootPublic, rootKey := newKey(t)
	devicePublic, deviceKey := newKey(t)
	_, session, _ := identityFixture(t)
	session.IdentityID = RootFingerprint(rootPublic)
	device := DeviceCertificate{Version: 1, IdentityID: session.IdentityID, DeviceID: session.DeviceID,
		DeviceEpoch: 1, SigningKey: devicePublic, NotBefore: session.NotBefore,
		ExpiresAt: session.NotBefore + DeviceMaxLifetime}
	for _, longDevice := range []bool{true, false} {
		d, s := device, session
		if longDevice {
			d.ExpiresAt++
		} else {
			s.ExpiresAt = s.NotBefore + SessionMaxLifetime + 1
		}
		dc := signAuthorityPayload(t, d, d.IdentityID, peercrypto.DeviceType, rootKey)
		sc := signAuthorityPayload(t, s, RootFingerprint(devicePublic), peercrypto.SessionType, deviceKey)
		chain := Chain{RootPublicKey: rootPublic, Device: dc, Session: sc}
		if _, err := VerifyChain(chain, session.IdentityID, fixtureNow); !fault.IsCode(err, fault.InvalidCertificate) {
			t.Errorf("oversized signed authority (device=%t): %v", longDevice, err)
		}
	}
}

func signAuthorityPayload(t testing.TB, value any, kid, typ string, key ed25519.PrivateKey) string {
	t.Helper()
	payload, err := codec.Canonical(value)
	if err != nil {
		t.Fatal(err)
	}
	compact, err := peercrypto.Sign(payload, kid, typ, key)
	if err != nil {
		t.Fatal(err)
	}
	return compact
}

func weakSigningKeys(t testing.TB) [][]byte {
	t.Helper()
	var keys [][]byte
	for _, value := range []string{
		"0000000000000000000000000000000000000000000000000000000000000000",
		"0100000000000000000000000000000000000000000000000000000000000000",
		"26e8958fc2b227b045c3f489f2ef98f0d5dfac05d3c63339b13802886d53fc05",
		"c7176a703d4dd84fba3c0b760d10670f2a2053fa2c39ccc64ec7fd7792ac037a",
		"ecffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff7f",
	} {
		key, err := hex.DecodeString(value)
		if err != nil {
			t.Fatal(err)
		}
		keys = append(keys, key)
		otherSign := bytes.Clone(key)
		otherSign[31] |= 0x80
		keys = append(keys, otherSign)
	}
	for _, first := range []byte{0xed, 0xee, 0xff} {
		key := bytes.Repeat([]byte{0xff}, 32)
		key[0], key[31] = first, 0x7f
		keys = append(keys, key)
		otherSign := bytes.Clone(key)
		otherSign[31] |= 0x80
		keys = append(keys, otherSign)
	}
	return keys
}
