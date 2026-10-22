package protocol_test

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/temirlankasen/peerturn/internal/fault"
	"github.com/temirlankasen/peerturn/internal/protocol"
)

func TestRoutingHeaderGolden(t *testing.T) {
	golden, err := os.ReadFile("../../api/fixtures/v1/routing-header.json")
	if err != nil {
		t.Fatal(err)
	}
	h, err := protocol.DecodeHeader(golden)
	if err != nil {
		t.Fatal(err)
	}
	got, err := h.Canonical()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, bytes.TrimSpace(golden)) {
		t.Fatalf("canonical header differs: %s", got)
	}
}

func TestHeaderRejectsAuthorityAndDowngrade(t *testing.T) {
	golden, err := os.ReadFile("../../api/fixtures/v1/routing-header.json")
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"role", "argv", "settings", "approval", "toolCall", "socket_path", "Role", "Wire_Version"} {
		t.Run(field, func(t *testing.T) {
			raw := append([]byte(`{"`+field+`":"owner",`), bytes.TrimSpace(golden)[1:]...)
			if _, err := protocol.DecodeHeader(raw); !fault.IsCode(err, fault.InvalidEncoding) {
				t.Fatalf("got %v", err)
			}
		})
	}
	for _, pair := range [][2]string{{`"wire_version":1`, `"wire_version":2`}, {`"feature_bits":0`, `"feature_bits":1`}} {
		raw := strings.Replace(string(golden), pair[0], pair[1], 1)
		if _, err := protocol.DecodeHeader([]byte(raw)); !fault.IsCode(err, fault.UnsupportedVersion) {
			t.Fatalf("got %v", err)
		}
	}
}

func FuzzDecodeHeader(f *testing.F) {
	golden, err := os.ReadFile("../../api/fixtures/v1/routing-header.json")
	if err != nil {
		f.Fatal(err)
	}
	f.Add(golden)
	f.Fuzz(func(t *testing.T, data []byte) {
		h, err := protocol.DecodeHeader(data)
		if err != nil {
			return
		}
		b, err := h.Canonical()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := protocol.DecodeHeader(b); err != nil {
			t.Fatal(err)
		}
	})
}
