package daemon

import (
	"strings"
	"testing"

	"github.com/temirlankasen/peerturn/internal/runtimepolicy"
	"github.com/temirlankasen/peerturn/internal/version"
)

// A message the peer has not fetched shows up against that peer, together
// with when the link lapses; a peer that never sent a heartbeat is unknown,
// not offline.
func TestPeersReportWaitingMessagesAndLinkExpiry(t *testing.T) {
	parallelDaemonTest(t)
	a, b := pairedStores(t)
	aid := peerID(b.state.Chain)
	commitData(t, a, b, "not fetched yet")
	peers, err := a.peerList(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(peers) != 1 || peers[0].ID != aid {
		t.Fatalf("peers %+v", peers)
	}
	p := peers[0]
	if p.WaitingOnRelay != 1 || p.WaitingSince == 0 || p.LinkExpiresAt <= unixNow() || p.Presence != "unknown" || p.PeerVersion != "" {
		t.Fatalf("peer facts %+v", p)
	}
}

// A heartbeat tells the peer the daemon is up and which PeerTurn it runs;
// it may carry nothing else.
func TestHeartbeatSetsPresenceAndVersion(t *testing.T) {
	parallelDaemonTest(t)
	a, b := pairedStores(t)
	bid := peerID(a.state.Chain)
	packet, err := a.handshakePacketTTL(a.state.Peers[peerID(b.state.Chain)], handshake{Heartbeat: true, Version: version.Version}, presenceTTL)
	if err != nil {
		t.Fatal(err)
	}
	if packet.Header.ExpiresAt > unixNow()+presenceTTL {
		t.Fatalf("heartbeat lives %d s, want at most %d", packet.Header.ExpiresAt-unixNow(), presenceTTL)
	}
	if err = b.receive(t.Context(), packet, 1); err != nil {
		t.Fatal(err)
	}
	peers, err := b.peerList(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if peers[0].Presence != "online" || peers[0].PeerVersion != version.Version || peers[0].LastSeenAt == 0 {
		t.Fatalf("heartbeat not reflected: %+v", peers[0])
	}
	smuggled, err := a.handshakePacketTTL(a.state.Peers[peerID(b.state.Chain)], handshake{Heartbeat: true, Version: "evil", Floor: 5}, presenceTTL)
	if err != nil {
		t.Fatal(err)
	}
	if err = b.receive(t.Context(), smuggled, 2); err != nil {
		t.Fatal(err)
	}
	if b.state.Peers[bid].PeerVersion != version.Version {
		t.Fatal("a heartbeat with a floor was accepted")
	}
	if a.state.Peers[peerID(b.state.Chain)].LastSeenAt != 0 {
		t.Fatal("the sender marked itself seen")
	}
}

// Every warning carries the next action, because the owner reads status to
// decide what to do, not to learn a code.
func TestStatusWarningsNameTheAction(t *testing.T) {
	t.Parallel()
	now := int64(1_800_000_000)
	report := runtimepolicy.NewReport(runtimepolicy.Claude, "9.9.9")
	s := Status{Binding: "needs_rebind", RuntimePolicy: report, RuntimeVersion: "9.9.9", OwnerInvitationHook: "not_observed", InDoubt: []string{"6b15d792af0dfe27599169226cb3849e"}, LastError: "unavailable", LastVerifiedAt: now - 7200, CertificateExpiresAt: now + 5*86400, LatestVersion: "9.0.0", PeerturnVersion: "0.2.0-preview"}
	peers := []Peer{{Fingerprint: "8cf596afb8df7aff", State: "connected", WaitingOnRelay: 2, WaitingSince: now - 3600, LinkExpiresAt: now + 86400}, {Fingerprint: "69c5cff4da398", State: "expired"}}
	warnings := statusWarnings(s, peers, now)
	joined := strings.Join(warnings, "\n")
	for _, want := range []string{
		"peerturn session bind --current",
		"newer than the last verified release",
		"Paste it again",
		"peerturn delivery resolve ID --sent or --not-sent",
		"Relay sync is failing with unavailable; last success 2 h ago",
		"certificate expires",
		"2 message(s) to 8cf596afb8df wait at the relay",
		"never heard from it",
		"lapses",
		"Link with 69c5cff4da39 expired: a new invitation is needed.",
		"run peerturn upgrade",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("warnings lack %q:\n%s", want, joined)
		}
	}
	summary := statusSummary(s, peers)
	if summary != "session binding being restored; hook not_observed; relay failing (unavailable); 2 link(s), 1 connected, 2 message(s) waiting at the relay" {
		t.Fatalf("summary %q", summary)
	}
	if got := statusWarnings(Status{Binding: "native_claude", OwnerInvitationHook: "active", CertificateExpiresAt: now + 300*86400}, nil, now); len(got) != 0 {
		t.Fatalf("healthy status warns: %v", got)
	}
}
