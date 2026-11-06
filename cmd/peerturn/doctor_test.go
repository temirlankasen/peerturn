package main

import (
	"strings"
	"testing"

	"github.com/temirlankasen/peerturn/internal/daemon"
)

// The checklist must say what is wrong and what to type, in that order:
// facts the owner can trust, then the actions that follow from them.
func TestRenderDoctorShowsFactsThenActions(t *testing.T) {
	now := int64(1_800_000_000)
	status := daemon.Status{
		PeerturnVersion: "0.2.0-preview", ProfileID: "854f51b20f53ce29", RelayURL: "https://relay.example",
		Binding: "needs_rebind", RuntimeVersion: "2.1.318", OwnerInvitationHook: "unbound",
		LastError: "unavailable", LastVerifiedAt: now - 3*3600, CertificateExpiresAt: now + 300*86400,
		InDoubt:  []string{"6b15d792af0dfe27"},
		Warnings: []string{"Session not bound: the plugin helper rebinds by itself within seconds.", "1 delivery(ies) in doubt (6b15d792af0d): decide with peerturn delivery resolve ID --sent or --not-sent."},
	}
	peers := []daemon.Peer{{Fingerprint: "8cf596afb8df7aff", State: "connected", LinkExpiresAt: now + 20*86400, Presence: "offline", PeerVersion: "0.2.0-preview", LastSeenAt: now - 2*86400, WaitingOnRelay: 1, WaitingSince: now - 3600}}
	out := renderDoctor(status, peers, now)
	for _, want := range []string{
		"PeerTurn 0.2.0-preview, profile 854f51b20f53…, relay https://relay.example",
		"✗ relay sync failing: unavailable (last success 3 h ago)",
		"✗ session not bound right now",
		"✗ prompt hook unbound",
		"✓ session certificate valid until",
		"✓ link 8cf596afb8df… connected · renews by use, lapses",
		"offline (PeerTurn 0.2.0-preview, seen 2 d ago)",
		"1 message(s) waiting at the relay since",
		"✗ 1 delivery(ies) in doubt",
		"Do next:\n  • Session not bound",
		"  • 1 delivery(ies) in doubt (6b15d792af0d): decide with peerturn delivery resolve ID --sent or --not-sent.",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("doctor output lacks %q:\n%s", want, out)
		}
	}
	facts, actions, _ := strings.Cut(out, "Do next:")
	if strings.Contains(facts, "delivery resolve") || !strings.Contains(actions, "delivery resolve") {
		t.Fatalf("actions are not after the facts:\n%s", out)
	}
	empty := renderDoctor(daemon.Status{PeerturnVersion: "dev", Binding: "test_session", Session: "lab"}, []daemon.Peer{}, now)
	if !strings.Contains(empty, "– no links yet: peerturn connect prints an invitation") || !strings.Contains(empty, "explicit preview session lab") {
		t.Fatalf("empty profile not explained:\n%s", empty)
	}
	if unknown := renderDoctor(daemon.Status{PeerturnVersion: "dev"}, nil, now); !strings.Contains(unknown, "not listed without a session") || strings.Contains(unknown, "no links yet") {
		t.Fatalf("doctor without a session claimed there are no links:\n%s", unknown)
	}
}
