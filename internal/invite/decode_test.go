package invite

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/temirlankasen/peerturn/internal/codec"
	"github.com/temirlankasen/peerturn/internal/fault"
)

func TestPacketFixtureAndStrictDecoders(t *testing.T) {
	data, err := os.ReadFile("../../api/fixtures/v1/invite-packet-synthetic.json")
	if err != nil {
		t.Fatal(err)
	}
	packet, err := DecodePacket(data)
	if err != nil {
		t.Fatal("synthetic shape fixture failed decoding", err)
	}
	if _, digestErr := PacketDigest(packet); digestErr != nil {
		t.Fatal(digestErr)
	}
	data, err = os.ReadFile("../../api/fixtures/v1/invite-packet-invalid.json")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodePacket(data); !fault.IsCode(err, fault.InvalidEncoding) {
		t.Fatal("authority-field fixture accepted", err)
	}
	f := newInviteFixture(t)
	valid := string(canonical(t, f.packet))
	for i, test := range []struct {
		data string
		code fault.Code
	}{
		{strings.Replace(valid, "{", `{"wire_version":1,`, 1), fault.InvalidEncoding},
		{strings.Replace(valid, "{", `{"role":"owner",`, 1), fault.InvalidEncoding},
		{strings.Replace(valid, `"wire_version":1`, `"wire_version":2`, 1), fault.UnsupportedVersion},
		{strings.Replace(valid, `"invite_id":`, `"invite_id":null,"unused":`, 1), fault.InvalidEncoding},
		{valid + "{}", fault.InvalidEncoding}, {strings.Repeat("x", MaxPacketBytes+1), fault.TooLarge},
	} {
		if _, err := DecodePacket([]byte(test.data)); !fault.IsCode(err, test.code) {
			t.Fatalf("invalid packet %d: expected %s, got %v", i, test.code, err)
		}
	}
	claimWire := canonical(t, f.claimWire)
	if _, err := DecodeClaimPacket(claimWire); err != nil {
		t.Fatal(err)
	}
	claimWire = bytes.Replace(claimWire, []byte(`"proof":`), []byte(`"proof":"duplicate","proof":`), 1)
	if _, err := DecodeClaimPacket(claimWire); !fault.IsCode(err, fault.InvalidEncoding) {
		t.Fatal("duplicate external proof accepted", err)
	}
}

func FuzzDecodePacket(f *testing.F) {
	fixture := newInviteFixture(f)
	f.Add(canonical(f, fixture.packet))
	f.Add([]byte(`{"wire_version":1,"wire_version":1}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		packet, err := DecodePacket(data)
		if err == nil {
			if _, err := PacketDigest(packet); err != nil {
				t.Fatal("accepted packet has no digest", err)
			}
		}
	})
}

func FuzzDecodeClaimPacket(f *testing.F) {
	fixture := newInviteFixture(f)
	f.Add(canonical(f, fixture.claimWire))
	f.Add([]byte(`{"packet":{},"proof":""}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		packet, err := DecodeClaimPacket(data)
		if err == nil {
			if _, err := DecodeClaimPacket(canonical(t, packet)); err != nil {
				t.Fatal("accepted claim packet failed round trip", err)
			}
		}
	})
}

func FuzzDecodeSignedContent(f *testing.F) {
	fixture := newInviteFixture(f)
	f.Add(canonical(f, signedContent{fixture.inviter.chain, descriptorCompact(f, fixture)}))
	f.Add(canonical(f, signedContent{fixture.claimant.chain, claimCompact(f, fixture)}))
	f.Fuzz(func(t *testing.T, data []byte) {
		content, err := decodeSignedContent(data)
		if err == nil {
			if _, decodeErr := decodeSignedContent(canonical(t, content)); decodeErr != nil {
				t.Fatal("accepted signed wrapper failed round trip", decodeErr)
			}
		}
	})
}

func FuzzOpenDescriptor(f *testing.F) {
	fixture := newInviteFixture(f)
	f.Add(canonical(f, fixture.packet))
	f.Fuzz(func(t *testing.T, data []byte) {
		var packet Packet
		if codec.Decode(data, MaxPacketBytes, &packet) == nil {
			descriptor, cert, err := OpenDescriptor(fixture.link, packet, fixture.descriptor.PublisherManifestHash, testNow)
			if err != nil && (descriptor.Version != 0 || cert.Version != 0) {
				t.Fatal("invalid descriptor exposed partial authority")
			}
		}
	})
}

func FuzzOpenClaim(f *testing.F) {
	fixture := newInviteFixture(f)
	f.Add(canonical(f, fixture.claimWire))
	f.Fuzz(func(t *testing.T, data []byte) {
		var packet ClaimPacket
		if codec.Decode(data, MaxPacketBytes, &packet) == nil {
			for _, pin := range []string{"", fixture.claimant.cert.IdentityID} {
				claim, err := OpenClaim(fixture.link, fixture.descriptor, packet, fixture.inviter.key, pin, 1, testNow)
				if err != nil && claim.Version != 0 {
					t.Fatal("invalid claim exposed partial authority")
				}
			}
		}
	})
}
