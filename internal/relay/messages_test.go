package relay

import (
	"bytes"
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	ptcrypto "github.com/temirlankasen/peerturn/internal/crypto"
	"github.com/temirlankasen/peerturn/internal/protocol"
)

func packet(t *testing.T, id int, plaintext string) protocol.EncryptedPacket {
	t.Helper()
	p, _, _ := packetWithKeys(t, id, plaintext)
	return p
}

func packetWithKeys(t *testing.T, id int, plaintext string) (protocol.EncryptedPacket, *ecdh.PrivateKey, ed25519.PrivateKey) {
	t.Helper()
	p := protocol.EncryptedPacket{Header: protocol.RoutingHeader{WireVersion: 1, Suite: protocol.Suite, DstMailbox: "box", RoutePermitID: "write", MessageID: fmt.Sprintf("%032x", id), RecipientKeyEpoch: 1, ExpiresAt: time.Now().Add(time.Hour).Unix()}}
	aad, err := p.Header.Canonical()
	if err != nil {
		t.Fatal(err)
	}
	privateKey, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer := key(t)
	signed, err := ptcrypto.Sign([]byte(plaintext), "fixture", ptcrypto.MessageType, signer)
	if err != nil {
		t.Fatal(err)
	}
	p.Enc, p.Ciphertext, err = ptcrypto.Seal(privateKey.PublicKey(), aad, []byte(signed))
	if err != nil {
		t.Fatal(err)
	}
	return p, privateKey, signer
}

func TestMessageCustodyDedupAndContiguousAck(t *testing.T) {
	f := newFixture(t)
	f.enroll(t)
	p := packet(t, 1, "fixture private body")
	f.request(t, "POST", "/v1/mailboxes/box/messages", p, f.reader, 403)
	first := f.request(t, "POST", "/v1/mailboxes/box/messages", p, f.writer, 200)
	again := f.request(t, "POST", "/v1/mailboxes/box/messages", p, f.writer, 200)
	if !bytes.Equal(first, again) {
		t.Fatalf("retry receipt changed: %s / %s", first, again)
	}
	p.Ciphertext[0] ^= 1
	f.request(t, "POST", "/v1/mailboxes/box/messages", p, f.writer, 409)
	p.Ciphertext[0] ^= 1
	f.request(t, "POST", "/v1/mailboxes/box/ack", map[string]int{"through": 1}, f.reader, 400)
	f.request(t, "GET", "/v1/mailboxes/box/messages?after=1", nil, f.reader, 400)
	data := f.request(t, "GET", "/v1/mailboxes/box/messages?after=0&limit=1", nil, f.reader, 200)
	var response struct {
		Messages []fetched `json:"messages"`
	}
	if err := json.Unmarshal(data, &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Messages) != 1 || response.Messages[0].Seq != 1 || response.Messages[0].Tombstone {
		t.Fatalf("bad fetch: %s", data)
	}
	f.request(t, "POST", "/v1/mailboxes/box/ack", map[string]int{"through": 2}, f.reader, 400)
	f.request(t, "POST", "/v1/mailboxes/box/ack", map[string]int{"through": 1}, f.reader, 200)
	f.request(t, "POST", "/v1/mailboxes/box/ack", map[string]int{"through": 1}, f.reader, 200)
	f.request(t, "POST", "/v1/mailboxes/box/messages", p, f.writer, 200)
	data = f.request(t, "GET", "/v1/mailboxes/box/messages?after=0", nil, f.reader, 200)
	if !bytes.Contains(data, []byte(`"tombstone":true`)) {
		t.Fatalf("missing custody tombstone: %s", data)
	}
	f.request(t, "DELETE", "/v1/mailboxes/box/permits/write", map[string]int{"epoch": 2}, f.reader, 200)
	f.request(t, "POST", "/v1/mailboxes/box/messages", p, f.writer, 200)
	p.Header.MessageID = "00000000000000000000000000000002"
	f.request(t, "POST", "/v1/mailboxes/box/messages", p, f.writer, 403)
}

func TestMessageSchemasLimitsAndBackpressure(t *testing.T) {
	f := newFixture(t)
	f.enroll(t)
	p := packet(t, 1, "limit test")
	p.Header.WireVersion = 2
	f.request(t, "POST", "/v1/mailboxes/box/messages", p, f.writer, 426)
	p.Header.WireVersion = 1
	p.Header.DstMailbox = "elsewhere"
	f.request(t, "POST", "/v1/mailboxes/box/messages", p, f.writer, 400)
	p.Header.DstMailbox = "box"
	p.Ciphertext = make([]byte, protocol.MaxCiphertext+1)
	f.request(t, "POST", "/v1/mailboxes/box/messages", p, f.writer, 413)
	f.request(t, "POST", "/v1/mailboxes/box/ack", map[string]any{"through": 0, "scope": "coauthor"}, f.reader, 400)
	f.request(t, "GET", "/v1/mailboxes/box/messages?after=0&after=1", nil, f.reader, 400)
	f.request(t, "GET", "/v1/mailboxes/box/messages?wait=26", nil, f.reader, 400)
	// A closed custody store must never produce a relay_stored receipt.
	if err := f.s.db.Close(); err != nil {
		t.Fatal(err)
	}
	data := f.request(t, "POST", "/v1/mailboxes/box/messages", packet(t, 2, "not stored"), f.writer, 503)
	if bytes.Contains(data, []byte("relay_stored")) {
		t.Fatal("storage failure acknowledged custody")
	}
}

func scan(t *testing.T, dir string, marker []byte) bool {
	t.Helper()
	root, openErr := os.OpenRoot(dir)
	if openErr != nil {
		t.Fatal(openErr)
	}
	defer func() {
		if closeErr := root.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	}()
	found := false
	err := filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		relative, relativeErr := filepath.Rel(dir, path)
		if relativeErr != nil {
			return relativeErr
		}
		data, readErr := root.ReadFile(relative)
		if readErr != nil {
			return readErr
		}
		found = found || bytes.Contains(data, marker)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return found
}

func TestCiphertextOnlyStorageWithPositiveControl(t *testing.T) {
	f := newFixture(t)
	f.enroll(t)
	markers := []string{"PRIVATE-BODY-08d6c707", "PRIVATE-SCOPE-coauthor-d993", "/private/native/session-c00c"}
	want := markers[0] + markers[1] + markers[2]
	p, recipient, signer := packetWithKeys(t, 1, want)
	f.request(t, "POST", "/v1/mailboxes/box/messages", p, f.writer, 200)
	data := f.request(t, "GET", "/v1/mailboxes/box/messages", nil, f.reader, 200)
	var response struct {
		Messages []fetched `json:"messages"`
	}
	if err := json.Unmarshal(data, &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Messages) != 1 {
		t.Fatal("receiver did not fetch ciphertext")
	}
	received, err := protocol.DecodePacket(response.Messages[0].Packet)
	if err != nil {
		t.Fatal(err)
	}
	aad, err := received.Header.Canonical()
	if err != nil {
		t.Fatal(err)
	}
	// This is the receiving client, outside the relay's production packages.
	compact, err := ptcrypto.Open(recipient, received.Enc, aad, received.Ciphertext)
	if err != nil {
		t.Fatal(err)
	}
	plaintext, err := ptcrypto.Verify(string(compact), "fixture", ptcrypto.MessageType, ed25519.PublicKey(signer[ed25519.SeedSize:]))
	if err != nil {
		t.Fatal(err)
	}
	if string(plaintext) != want {
		t.Fatal("receiver plaintext changed")
	}
	for _, marker := range markers {
		if scan(t, f.s.cfg.DataDir, []byte(marker)) {
			t.Fatalf("plaintext persisted: %s", marker)
		}
	}
	control := filepath.Join(f.s.cfg.DataDir, "positive-control")
	if err := os.WriteFile(control, []byte(markers[0]), 0o600); err != nil {
		t.Fatal(err)
	}
	if !scan(t, f.s.cfg.DataDir, []byte(markers[0])) {
		t.Fatal("scanner missed positive control")
	}
	if err := os.Remove(control); err != nil {
		t.Fatal(err)
	}
}
