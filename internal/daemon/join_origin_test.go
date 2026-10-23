package daemon

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/temirlankasen/peerturn/internal/invite"
	"github.com/temirlankasen/peerturn/internal/localipc"
	"github.com/temirlankasen/peerturn/internal/protocol"
	"github.com/temirlankasen/peerturn/internal/store"
)

func TestJoinRequiresNativeProofAndOwnerPrompt(t *testing.T) {
	parallelDaemonTest(t)
	d, _ := pairedStores(t)
	bindClaude(t, d)
	link := syntheticJoinLink("foreign.invalid", "A")
	capability := joinCapability(t, d)
	r := textJoinRequest(t, link)
	r.Session, r.Capability = d.state.Label, capability
	if _, err := d.Dispatch(t.Context(), r); localipc.PublicError(err).Code != "needs_rebind" {
		t.Fatalf("label-only proxy acquired join authority: %v", err)
	}
	resolved, err := d.Dispatch(t.Context(), localipc.Request{Method: "adapter.resolve", NativeSocket: d.claudeBinding.Socket, Nonce: d.claudeBinding.ConfirmedNonce})
	if err != nil {
		t.Fatal(err)
	}
	var proof struct {
		Epoch string `json:"binding_epoch"`
	}
	decodeResult(t, resolved, &proof)
	raw, err := json.Marshal(map[string]string{"method": "hello", "session": d.state.Label, "binding_epoch": proof.Epoch})
	r = localipc.Request{}
	if err != nil || json.Unmarshal(raw, &r) != nil {
		t.Fatal("invalid fixture")
	}
	result, err := d.Dispatch(t.Context(), r)
	if err != nil {
		t.Fatal(err)
	}
	var hello struct {
		Capability string `json:"capability"`
	}
	decodeResult(t, result, &hello)
	r = textJoinRequest(t, link)
	r.Session, r.Capability = d.state.Label, hello.Capability
	if _, err = d.Dispatch(t.Context(), r); localipc.PublicError(err).Code != "no_owner_invitation" {
		t.Fatalf("invitation absent from owner prompt reached onboarding: %v", err)
	}
}

func TestMCPBindingEpochCannotAuthorizeAdapterIPC(t *testing.T) {
	parallelDaemonTest(t)
	d, _ := pairedStores(t)
	instance := bindClaude(t, d)
	resolved, err := d.Dispatch(t.Context(), localipc.Request{Method: "adapter.resolve", NativeSocket: d.claudeBinding.Socket, Nonce: d.claudeBinding.ConfirmedNonce})
	if err != nil {
		t.Fatal(err)
	}
	var proof struct {
		Epoch string `json:"binding_epoch"`
	}
	decodeResult(t, resolved, &proof)
	if proof.Epoch == "" || proof.Epoch == instance {
		t.Fatal("native resolve exposed the helper bearer")
	}
	if _, err = d.Dispatch(t.Context(), localipc.Request{Method: "adapter.close", Instance: proof.Epoch}); localipc.PublicError(err).Code != "needs_rebind" {
		t.Fatalf("MCP epoch authorized helper control: %v", err)
	}
}

func TestDeliveredPeerInvitationsCannotCreateAuthority(t *testing.T) {
	parallelDaemonTest(t)
	for _, kind := range []protocol.Kind{protocol.Coauthor, protocol.Inform, protocol.Reply} {
		t.Run(string(kind), func(t *testing.T) {
			a, b := pairedStores(t)
			bindClaude(t, a)
			link := syntheticJoinLink("foreign.invalid", "A")
			var parent *store.InboxEntry
			if kind == protocol.Reply {
				packet := incomingData(t, a, b)
				a.state.Reserved = &packet
				a.state.Sequences[packet.PeerID]++
				if err := a.recoverReserved(t.Context()); err != nil {
					t.Fatal(err)
				}
				if err := b.receive(t.Context(), packet.Packet, 1); err != nil {
					t.Fatal(err)
				}
				// Deliver the signed receipt before the next sender sequence.
				for id := range b.state.Pending {
					raw, err := b.db.OutboxPacket(t.Context(), id)
					if err != nil {
						t.Fatal(err)
					}
					pending, err := protocol.DecodePacket(raw)
					if err != nil {
						t.Fatal(err)
					}
					if err := a.receive(t.Context(), pending, 1); err != nil {
						t.Fatal(err)
					}
				}
				entry, err := b.db.InboxEntry(t.Context(), packet.Message.MessageID)
				if err != nil {
					t.Fatal(err)
				}
				parent = &entry
			}
			p, err := b.makePacket(peerID(a.state.Chain), b.state.Peers[peerID(a.state.Chain)], kind, "peer data: "+link, parent)
			if err != nil {
				t.Fatal(err)
			}
			seq := uint64(1)
			if parent != nil {
				seq = 2
			}
			if err = a.receive(t.Context(), p.Packet, seq); err != nil {
				t.Fatal(err)
			}
			if _, err = a.db.InboxEntry(t.Context(), p.Message.MessageID); err != nil {
				t.Fatalf("fixture did not admit the peer body: %v", err)
			}
			if _, err = a.Dispatch(t.Context(), localipc.Request{Method: "inbox", Session: a.state.Label}); err != nil {
				t.Fatal(err)
			}
			// Reordering fragment fields must not disguise the same invitation.
			parts := strings.Split(link, "#v=1&")
			variant := parts[0] + "#" + parts[1] + "&v=1"
			for _, candidate := range []string{link, variant} {
				_, err = a.Dispatch(t.Context(), localipc.Request{Method: "join", Session: a.state.Label, Scope: "coauthor", Link: candidate})
				if localipc.PublicError(err).Code != "peer_invitation_denied" {
					t.Fatalf("peer invitation reached onboarding: %v", err)
				}
			}
			if len(a.state.Joining) != 0 {
				t.Fatal("rejected peer invitation changed onboarding state")
			}
		})
	}
}

func provenJoinCapability(t *testing.T, d *Daemon) string {
	t.Helper()
	if d.claudeBinding == nil {
		bindClaude(t, d)
	}
	result, err := d.Dispatch(t.Context(), localipc.Request{Method: "hello", Session: d.state.Label, BindingEpoch: d.claudeBinding.MCPEpoch()})
	if err != nil {
		t.Fatal(err)
	}
	var hello struct {
		Capability string `json:"capability"`
	}
	decodeResult(t, result, &hello)
	return hello.Capability
}

func ownerPastedInvite(t *testing.T, d *Daemon, text string) {
	t.Helper()
	b := d.claudeBinding
	_, err := d.Dispatch(t.Context(), localipc.Request{Method: "adapter.boundary", NativeSession: b.NativeSessionID, NativeSocket: b.Socket, Instance: b.Instance, OwnerInvites: invite.PromptLinks(text)})
	if err != nil {
		t.Fatal(err)
	}
}

func TestOwnerInvitationExpiresAndCannotFollowNewIncarnation(t *testing.T) {
	parallelDaemonTest(t)
	d, _ := pairedStores(t)
	capability := provenJoinCapability(t, d)
	link := syntheticJoinLink("foreign.invalid", "A")
	ownerPastedInvite(t, d, link)
	if err := d.checkInvitationOrigin(link, capability); err != nil {
		t.Fatal(err)
	}
	hash, _, err := invite.OriginHashes(link)
	if err != nil {
		t.Fatal(err)
	}
	record := d.joinOrigins.owner[hash]
	record.expires = time.Now().Add(-time.Second)
	d.joinOrigins.owner[hash] = record
	if err = d.checkInvitationOrigin(link, capability); localipc.PublicError(err).Code != "owner_invitation_expired" {
		t.Fatalf("expired owner input retained authority: %v", err)
	}
	ownerPastedInvite(t, d, link)
	if _, err = d.Dispatch(t.Context(), localipc.Request{Method: "adapter.close", Instance: d.claudeBinding.Instance}); err != nil {
		t.Fatal(err)
	}
	bindClaude(t, d)
	if err = d.checkInvitationOrigin(link, provenJoinCapability(t, d)); localipc.PublicError(err).Code != "no_owner_invitation" {
		t.Fatalf("new native incarnation inherited owner authority: %v", err)
	}
}

func TestPeerInvitationVolumeDoesNotBlockOwner(t *testing.T) {
	parallelDaemonTest(t)
	d, _ := pairedStores(t)
	capability := provenJoinCapability(t, d)
	for i := range 1100 {
		d.recordPeerInvites("sender-a", syntheticJoinLink(fmt.Sprintf("peer%d.invalid", i), "A"))
	}
	link := syntheticJoinLink("relay.example", "A")
	ownerPastedInvite(t, d, link)
	for _, cap := range []string{"", capability} {
		if err := d.checkInvitationOrigin(link, cap); err != nil {
			t.Fatalf("unrelated owner invitation blocked by peer volume: %v", err)
		}
	}
}

func TestPeerPageLinkStillBlocksInvitationClaim(t *testing.T) {
	parallelDaemonTest(t)
	d, _ := pairedStores(t)
	link := syntheticJoinLink("relay.example", "A")
	page, _, _ := strings.Cut(link, "#")
	d.recordPeerInvites("sender-a", "peer page: <"+page+">")
	if err := d.checkInvitationOrigin(link, ""); localipc.PublicError(err).Code != "peer_invitation_denied" {
		t.Fatalf("adding a fragment disguised a peer invitation claim: %v", err)
	}
}

func TestPeerCaseVariantLinkBlocksClaim(t *testing.T) {
	parallelDaemonTest(t)
	for _, prefix := range []string{"HTTPS://relay.example", "https://RELAY.EXAMPLE", "relay.example"} {
		t.Run(prefix, func(t *testing.T) {
			d, _ := pairedStores(t)
			link := syntheticJoinLink("relay.example", "A")
			d.recordPeerInvites("sender-a", strings.Replace(link, "https://relay.example", prefix, 1))
			if err := d.checkInvitationOrigin(link, ""); localipc.PublicError(err).Code != "peer_invitation_denied" {
				t.Fatalf("URL case disguised a peer invitation claim: %v", err)
			}
		})
	}
}

func TestEncodedPeerLinkBlocksCanonicalClaim(t *testing.T) {
	parallelDaemonTest(t)
	link := syntheticJoinLink("relay.example", "A")
	page, _, _ := strings.Cut(link, "#")
	for _, text := range []string{
		"(RELAY.EXAMPLE/i/%41" + strings.TrimPrefix(strings.Split(page, "/i/")[1], "A") + ").",
		"<" + strings.ReplaceAll(strings.ReplaceAll(page, ":", "%3A"), "/", "%2f") + ">",
		"[" + strings.ReplaceAll(page, "/i/", "/%69/") + "];",
	} {
		t.Run(text, func(t *testing.T) {
			d, _ := pairedStores(t)
			d.recordPeerInvites("sender-a", text)
			if err := d.checkInvitationOrigin(link, ""); localipc.PublicError(err).Code != "peer_invitation_denied" {
				t.Fatalf("encoded peer claim was not recognized: %v", err)
			}
		})
	}
}
