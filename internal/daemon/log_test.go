package daemon

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"github.com/temirlankasen/peerturn/internal/protocol"
)

// An operator reads the log to learn why a packet was dropped; the line
// names the slot and the code, never the ciphertext or a body.
func TestRejectedPacketIsLoggedByCodeOnly(t *testing.T) {
	parallelDaemonTest(t)
	a, b := pairedStores(t)
	var buf bytes.Buffer
	b.log = slog.New(slog.NewTextHandler(&buf, nil))
	r := commitData(t, a, b, "private body")
	r.Packet.Ciphertext = append([]byte{}, r.Packet.Ciphertext...)
	r.Packet.Ciphertext[0] ^= 0xff
	if err := b.receive(t.Context(), r.Packet, 1); err != nil {
		t.Fatal(err)
	}
	line := buf.String()
	if !strings.Contains(line, "packet rejected") || !strings.Contains(line, "seq=1") {
		t.Fatalf("rejection not logged: %q", line)
	}
	if strings.Contains(line, "private body") {
		t.Fatalf("log carries a body: %q", line)
	}
	buf.Reset()
	good := commitData(t, a, b, "second")
	if err := b.receive(t.Context(), good.Packet, 2); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "message admitted") || !strings.Contains(buf.String(), "kind="+string(protocol.Coauthor)) {
		t.Fatalf("admission not logged: %q", buf.String())
	}
	if strings.Contains(buf.String(), "second") {
		t.Fatalf("log carries a body: %q", buf.String())
	}
}
