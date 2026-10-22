package protocol_test

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"

	"github.com/temirlankasen/peerturn/internal/protocol"
)

func TestGoldenWireObjects(t *testing.T) {
	for _, name := range []string{"message", "receipt", "packet"} {
		t.Run(name, func(t *testing.T) {
			var raw []byte
			var err error
			switch name {
			case "message":
				raw, err = os.ReadFile("../../api/fixtures/v1/message.json")
			case "receipt":
				raw, err = os.ReadFile("../../api/fixtures/v1/receipt.json")
			case "packet":
				raw, err = os.ReadFile("../../api/fixtures/v1/packet.json")
			}
			if err != nil {
				t.Fatal(err)
			}
			var value any
			switch name {
			case "message":
				value, err = protocol.DecodeMessage(raw)
			case "receipt":
				value, err = protocol.DecodeReceipt(raw)
			case "packet":
				value, err = protocol.DecodePacket(raw)
			}
			if err != nil {
				t.Fatal(err)
			}
			got, err := json.Marshal(value)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, bytes.TrimSpace(raw)) {
				t.Fatal("wire representation changed")
			}
		})
	}
}
