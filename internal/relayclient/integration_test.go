package relayclient

import (
	"crypto/ed25519"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/temirlankasen/peerturn/internal/codec"
	ptcrypto "github.com/temirlankasen/peerturn/internal/crypto"
	"github.com/temirlankasen/peerturn/internal/invite"
	"github.com/temirlankasen/peerturn/internal/relay"
	"github.com/temirlankasen/peerturn/internal/relay/config"
)

func realRelay(t *testing.T) (*httptest.Server, *Client) {
	t.Helper()
	key := testKey(t)
	s := httptest.NewUnstartedServer(nil)
	cfg := config.Defaults()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cfg.DataDir = filepath.Join(root, "relay")
	cfg.PublicAuthority = s.Listener.Addr().String()
	cfg.AdminKeys = []string{keyID(key)}
	cfg.TrustedProxies = []string{"127.0.0.1/32"}
	cfg.Limits.RateBurst, cfg.Limits.RatePerMinute = 100000, 100000
	r, err := relay.New(cfg)
	if err != nil {
		s.Close()
		t.Fatal(err)
	}
	s.Config.Handler = r.Handler()
	s.StartTLS()
	t.Cleanup(func() {
		s.Close()
		if closeErr := r.Close(t.Context()); closeErr != nil {
			t.Error(closeErr)
		}
	})
	c, err := New(Options{URL: s.URL, Key: key, HTTPClient: s.Client()})
	if err != nil {
		t.Fatal(err)
	}
	return s, c
}

func relayClient(t *testing.T, s *httptest.Server, key ed25519.PrivateKey) *Client {
	t.Helper()
	c, err := New(Options{URL: s.URL, Key: key, HTTPClient: s.Client()})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestActualRelayCustodyAndInviteLifecycle(t *testing.T) {
	s, admin := realRelay(t)
	readerKey, writerKey, proofKey := testKey(t), testKey(t), testKey(t)
	reader, writer := relayClient(t, s, readerKey), relayClient(t, s, writerKey)
	ctx := t.Context()
	discovery, err := admin.Discovery(ctx)
	if err != nil {
		t.Fatal(err)
	}
	consistent, err := New(Options{URL: s.URL, Key: readerKey, HTTPClient: s.Client(), ExpectedRelayKey: discovery.RelayKey})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = admin.EnrollMailbox(ctx, MailboxRequest{ID: "box", ReadKey: keyID(readerKey)}); err != nil {
		t.Fatal(err)
	}
	if err = reader.PutPermit(ctx, "box", "permit", Permit{WriterKey: keyID(writerKey), Epoch: 1, ExpiresAt: time.Now().Add(time.Hour).Unix()}); err != nil {
		t.Fatal(err)
	}
	packet := testPacket()
	for range 2 {
		receipt, sendErr := writer.Send(ctx, packet)
		if sendErr != nil || receipt.Seq != 1 {
			t.Fatalf("immutable retry: %+v %v", receipt, sendErr)
		}
	}
	messages, err := consistent.Fetch(ctx, "box", FetchOptions{})
	if err != nil || len(messages) != 1 || messages[0].Packet == nil {
		t.Fatalf("fetch: %+v %v", messages, err)
	}
	if err = reader.Ack(ctx, "box", 1); err != nil {
		t.Fatal(err)
	}
	if _, err = reader.MintTicket(ctx, "box"); err != nil {
		t.Fatal(err)
	}
	if err = reader.RevokePermit(ctx, "box", "permit", 2); err != nil {
		t.Fatal(err)
	}
	packet.Header.MessageID = strings.Repeat("b", 32)
	if _, err = writer.Send(ctx, packet); err == nil {
		t.Fatal("revoked writer retained send authority")
	}

	id := "AAAAAAAAAAAAAAAAAAAAAA"
	ip := invite.Packet{Version: 1, InviteID: id, Enc: make([]byte, 32), Ciphertext: make([]byte, 16)}
	expires := time.Now().Add(10 * time.Minute).Unix()
	if err = admin.CreateInvite(ctx, InviteRequest{ID: id, ProofKey: keyID(proofKey), Packet: ip, ExpiresAt: expires, Voucher: strings.Repeat("v", 32)}); err != nil {
		t.Fatal(err)
	}
	fetched, err := writer.GetInvite(ctx, id)
	if err != nil || fetched.ClaimPacket != nil {
		t.Fatalf("invite fetch: %+v %v", fetched, err)
	}
	digest, err := invite.PacketDigest(ip)
	if err != nil {
		t.Fatal(err)
	}
	proof, err := codec.Canonical(map[string]any{"wire_version": 1, "invite_id": id, "claim_id": id, "reader_key": keyID(writerKey), "ciphertext_hash": digest, "expires_at": expires})
	if err != nil {
		t.Fatal(err)
	}
	jws, err := ptcrypto.Sign(proof, id, ptcrypto.InviteProofType, proofKey)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = writer.ClaimInvite(ctx, id, invite.ClaimPacket{Packet: ip, Proof: jws}); err != nil {
		t.Fatal(err)
	}
	fetched, err = relayClient(t, s, proofKey).GetInvite(ctx, id)
	if err != nil || fetched.ClaimPacket == nil {
		t.Fatalf("claim lookup: %+v %v", fetched, err)
	}
	if _, err = writer.EnrollMailbox(ctx, MailboxRequest{ID: "joined", ReadKey: keyID(writerKey), Voucher: strings.Repeat("v", 32)}); err != nil {
		t.Fatal(err)
	}
	if err = reader.DeleteMailbox(ctx, "box"); err != nil {
		t.Fatal(err)
	}
}
