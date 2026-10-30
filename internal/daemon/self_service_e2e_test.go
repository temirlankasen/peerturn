package daemon

import (
	"bytes"
	"reflect"
	"testing"
	"time"

	"github.com/temirlankasen/peerturn/internal/codec"
	"github.com/temirlankasen/peerturn/internal/localipc"
)

// Synthetic preview sessions prove daemon/relay custody only. They do not prove
// Claude/Codex binding, owner-pasted MCP provenance, model input, completion,
// reply, or a Haiku installation.
func TestSelfServiceE2EClosedAndOptInNonAdminPair(t *testing.T) {
	parallelDaemonTest(t)
	operator := publicKey(routingKey(t)[32:])
	t.Run("default_closed", func(t *testing.T) { selfServiceE2EClosed(t, operator) })
	t.Run("self_service", func(t *testing.T) { selfServiceE2EOpen(t, operator) })
}

func selfServiceE2EClosed(t *testing.T, operator string) {
	t.Helper()
	f := newSelfServiceE2ERelay(t, operator, false)
	a := openSelfServiceE2EDaemon(t, f, "synthetic-closed-inviter")
	b := openSelfServiceE2EDaemon(t, f, "synthetic-closed-recipient")
	if equalBytes(a.routingPublic(), b.routingPublic()) {
		t.Fatal("fresh daemon routing keys must be independent")
	}
	for _, d := range []*Daemon{a, b} {
		_, err := d.Dispatch(t.Context(), localipc.Request{Method: "invite", Session: d.state.Label, Scope: "coauthor"})
		if safeCode(err) != "forbidden" {
			t.Fatal("closed relay did not refuse nonadmin invitation:", safeCode(err))
		}
		assertSelfServiceE2EEnrollment(t, d, false)
		if len(d.state.Issued) != 0 || len(d.state.Joining) != 0 || len(d.state.Peers) != 0 {
			t.Fatal("closed refusal retained onboarding authority")
		}
		assertSelfServiceE2ECounts(t, f, selfServiceE2ECounts{})
	}
}

func selfServiceE2EOpen(t *testing.T, operator string) {
	t.Helper()
	f := newSelfServiceE2ERelay(t, operator, true)
	a := openSelfServiceE2EDaemon(t, f, "synthetic-self-service-inviter")
	b := openSelfServiceE2EDaemon(t, f, "synthetic-self-service-recipient")
	if equalBytes(a.routingPublic(), b.routingPublic()) {
		t.Fatal("fresh daemon routing keys must be independent")
	}
	assertSelfServiceE2ECounts(t, f, selfServiceE2ECounts{})
	var invitation struct {
		URL     string `json:"url"`
		Binding string `json:"binding"`
	}
	// No fixture enrollment, provisioning request or ACL exists before invite.
	selfServiceE2ECall(t, a, localipc.Request{Method: "invite", Scope: "coauthor"}, &invitation)
	if invitation.URL == "" || invitation.Binding != "test_session" || len(a.state.Issued) != 1 {
		t.Fatal("first nonadmin publication did not return a synthetic-session invitation")
	}
	var issued *issuedInvite
	for _, entry := range a.state.Issued {
		issued = entry
	}
	if issued == nil || issued.URL != invitation.URL || issued.Claim != nil || issued.Sent {
		t.Fatal("fresh invitation journal is inconsistent")
	}
	id := issued.Descriptor.InviteID
	assertSelfServiceE2EEnrollment(t, a, true)
	assertSelfServiceE2EEnrollment(t, b, false)
	own := selfServiceE2ECounts{inviters: 1, mailboxes: 1, invites: 1}
	assertSelfServiceE2ECounts(t, f, own)
	var readKey string
	if err := f.db.QueryRowContext(t.Context(), "SELECT read_key FROM mailboxes WHERE id=?", a.mailbox()).Scan(&readKey); err != nil || readKey != publicKey(a.routingPublic()) {
		t.Fatal("publication did not create exactly the signer's derived mailbox")
	}
	before := selfServiceE2EStoredPublication(t, f, id)
	packet, err := codec.Canonical(issued.Packet)
	if err != nil || !bytes.Equal(before.packet, packet) || before.owner != readKey || before.proof == readKey || before.created <= 0 || before.expires != issued.Descriptor.ExpiresAt*int64(time.Second) || before.revoked || len(before.voucher) == 0 {
		t.Fatal("durable publication lost authenticated ownership, ciphertext or original TTL")
	}
	// Replay the retained journal entry through real signed HTTP with a fresh
	// nonce, without generating another invitation, packet or enrollment voucher.
	if err = a.publishInvite(t.Context(), issued); err != nil {
		t.Fatal("identical signed publication replay failed:", safeCode(err))
	}
	assertSelfServiceE2ECounts(t, f, own)
	if !reflect.DeepEqual(before, selfServiceE2EStoredPublication(t, f, id)) {
		t.Fatal("publication replay changed durable creation, bytes or TTL")
	}
	selfServiceE2ECall(t, b, localipc.Request{Method: "join", Scope: "coauthor", Link: invitation.URL}, nil)
	joining := b.state.Joining[id]
	if joining == nil || joining.Claimed || joining.Enrolled || joining.Complete {
		t.Fatal("join skipped the normal pending claim/voucher path")
	}
	assertSelfServiceE2EEnrollment(t, b, false)
	assertSelfServiceE2ECounts(t, f, own)
	if err = b.syncWithBudget(t.Context(), 30*time.Second); err != nil {
		t.Fatal("normal claim and voucher enrollment failed:", safeCode(err))
	}
	if !joining.Claimed || !joining.Enrolled || joining.Complete {
		t.Fatal("recipient did not retain claim and voucher admission before hello")
	}
	assertSelfServiceE2EEnrollment(t, b, true)
	assertSelfServiceE2ECounts(t, f, selfServiceE2ECounts{inviters: 1, mailboxes: 2, invites: 1, permits: 1})
	var claimID, claimReader, enrolled string
	var claimPacket []byte
	err = f.db.QueryRowContext(t.Context(), `SELECT claim_id,claim_reader,enrolled_mailbox,claim_packet FROM invites WHERE id=?`, id).
		Scan(&claimID, &claimReader, &enrolled, &claimPacket)
	if err != nil || claimID != joining.Claim.ClaimID || claimReader != publicKey(b.routingPublic()) || enrolled != b.mailbox() || len(claimPacket) == 0 {
		t.Fatal("real relay did not bind claim and voucher to the recipient routing signer")
	}
	selfServiceE2ESyncUntil(t, a, b, func() bool {
		ap, bp := a.peers(), b.peers()
		return len(ap) == 1 && len(bp) == 1 && ap[0].State == "connected" && bp[0].State == "connected"
	})
	if issued.Claim == nil || !issued.Sent || !joining.Complete || !joining.AckSent {
		t.Fatal("connected state skipped committed claim, grants or hello ACK")
	}
	assertSelfServiceE2EGrants(t, a, b)
	assertSelfServiceE2EGrants(t, b, a)
	assertSelfServiceE2ECounts(t, f, selfServiceE2ECounts{inviters: 1, mailboxes: 2, invites: 1, permits: 2})
	forward := "synthetic-self-service-forward-plaintext-8143 @path https://example.invalid is literal"
	reverse := "synthetic-self-service-reverse-plaintext-2769"
	selfServiceE2ECustody(t, f, a, b, forward)
	selfServiceE2ECustody(t, f, b, a, reverse)
	selfServiceE2ERevoke(t, f, a, b)
	if !reflect.DeepEqual(before, selfServiceE2EStoredPublication(t, f, id)) {
		t.Fatal("claim/transport/revoke changed original publication creation metadata")
	}
	assertPrivateState(t, a.options.StateDir)
	assertPrivateState(t, b.options.StateDir)
	assertRelayEncrypted(t, f.cfg.DataDir, []string{forward, reverse, a.state.Label, b.state.Label, a.cert.SessionID, b.cert.SessionID, "coauthor", invitation.URL})
}
