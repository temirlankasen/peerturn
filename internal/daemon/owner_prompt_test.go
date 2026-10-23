package daemon

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/temirlankasen/peerturn/internal/localipc"
)

func TestOwnerPromptPartialAndPeerOnlyLinksNeverBecomePending(t *testing.T) {
	parallelDaemonTest(t)
	d, _ := pairedStores(t)
	capability := provenJoinCapability(t, d)
	link := syntheticJoinLink("relay.example", "A")
	for _, text := range []string{
		strings.Replace(link, "&fp="+strings.Repeat("a", 64), "&fp="+strings.Repeat("a", 63), 1),
		strings.Replace(link, "&ek="+strings.Repeat("A", 43), "&ek="+strings.Repeat("A", 42), 1),
		strings.Split(link, "#")[0],
		"tool output is never owner input",
	} {
		ownerPastedInvite(t, d, text)
		_, err := d.Dispatch(t.Context(), pendingJoinRequest(t, d, capability, ""))
		if localipc.PublicError(err).Code != "no_owner_invitation" || len(d.joinOrigins.owner) != 0 {
			t.Fatal("partial owner link acquired authority")
		}
	}
	d.recordPeerInvites("peer", link)
	_, err := d.Dispatch(t.Context(), pendingJoinRequest(t, d, capability, ""))
	if localipc.PublicError(err).Code != "no_owner_invitation" {
		t.Fatal("peer-only link became a pending owner invitation")
	}
	ownerPastedInvite(t, d, link)
	_, err = d.Dispatch(t.Context(), pendingJoinRequest(t, d, capability, ""))
	if localipc.PublicError(err).Code != "peer_invitation_denied" || len(d.joinOrigins.owner) != 0 {
		t.Fatal("retained peer claim bypassed the deny fence")
	}
}

func TestOwnerPendingBoundDedupAndExpiryClearsBuffer(t *testing.T) {
	parallelDaemonTest(t)
	d, _ := pairedStores(t)
	provenJoinCapability(t, d)
	for i := range 12 {
		ownerPastedInvite(t, d, syntheticJoinLink(fmt.Sprintf("relay%d.example", i), "A"))
	}
	if len(d.joinOrigins.owner) != 8 {
		t.Fatal("pending owner storage is not bounded")
	}
	var retained [][]byte
	for hash, r := range d.joinOrigins.owner {
		retained = append(retained, r.link)
		ownerPastedInvite(t, d, string(r.link))
		if !d.joinOrigins.owner[hash].expires.Equal(r.expires) {
			t.Fatal("duplicate owner link extended capability lifetime")
		}
	}
	d.expireOwnerInvites(time.Now().Add(11 * time.Minute))
	for _, secret := range retained {
		if strings.Trim(string(secret), "\x00") != "" {
			t.Fatal("expiry retained capability bytes")
		}
	}
	if len(d.joinOrigins.owner) != 0 {
		t.Fatal("expired capability remained indexed")
	}
}

func TestMalformedCLIInvitationExplainsRecoveryWithoutEcho(t *testing.T) {
	parallelDaemonTest(t)
	_, err := invitationText("https://relay.example/i/incomplete#private-capability")
	problem := localipc.PublicError(err)
	if problem.Code != "malformed_invitation" || !strings.Contains(problem.Message, "malformed or incomplete") || !strings.Contains(problem.Message, "unchanged") || strings.Contains(problem.Message, "private-capability") {
		t.Fatal("CLI malformed link lacks sanitized recovery")
	}
}

func TestAlteredOwnerCapabilityFailsAuthenticationWithoutAuthority(t *testing.T) {
	parallelDaemonTest(t)
	admin := routingKey(t)
	relay := newRelayFixture(t, admin)
	issuer := openRecoveryDaemon(t, relay, admin)
	d := openRecoveryDaemon(t, relay, routingKey(t))
	created, err := issuer.createInvite(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	var invitation struct{ URL string }
	decodeResult(t, created, &invitation)
	capability := provenJoinCapability(t, d)
	for _, marker := range []string{"&fp=", "&ek="} {
		before, secret, ok := strings.Cut(invitation.URL, marker)
		if !ok {
			t.Fatal("fixture lacks capability field")
		}
		replacement := byte('a')
		if secret[0] == replacement {
			replacement = 'b'
		}
		altered := before + marker + string(replacement) + secret[1:]
		ownerPastedInvite(t, d, altered)
		_, err = d.Dispatch(t.Context(), pendingJoinRequest(t, d, capability, ""))
		if err == nil || len(d.state.Joining) != 0 || len(d.state.Peers) != 0 || len(d.joinOrigins.owner) != 0 {
			t.Fatal("altered owner capability created peer authority")
		}
	}
}
