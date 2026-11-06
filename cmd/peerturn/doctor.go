package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/temirlankasen/peerturn/internal/daemon"
	"github.com/temirlankasen/peerturn/internal/localipc"
)

// runDoctor prints facts first and actions last. An owner who reads it
// learns in one screen what works, what waits on whom, and what to type.
func runDoctor(ctx context.Context, client localipc.Client, request localipc.Request, stdout, stderr io.Writer) int {
	now := time.Now().Unix()
	raw, err := client.Call(ctx, localipc.Request{Method: "status", Session: request.Session, NativeThread: request.NativeThread})
	if err != nil {
		if _, writeErr := io.WriteString(stdout, "✗ daemon not reachable: "+localipc.PublicError(err).Message+"\n"); writeErr != nil {
			return 1
		}
		return writeMCPFailure(stderr, err)
	}
	var status daemon.Status
	if json.Unmarshal(raw, &status) != nil {
		return writeFailure(stdout, stderr, localipc.NewError("invalid_response", "unavailable"))
	}
	// Links belong to a session; without one, or when the list cannot be
	// read, the checklist says so instead of claiming there are none.
	var peers []daemon.Peer
	linksNote := "– links: not listed without a session; run peerturn doctor --current from the bound session"
	if request.Session != "" {
		raw, err = client.Call(ctx, localipc.Request{Method: "peers", Session: request.Session, NativeThread: request.NativeThread})
		switch {
		case err != nil:
			linksNote = "✗ links could not be listed: " + localipc.PublicError(err).Message
		case json.Unmarshal(raw, &peers) != nil:
			peers, linksNote = nil, "✗ links could not be listed: malformed answer"
		case peers == nil:
			peers = []daemon.Peer{}
		}
	}
	if _, err = io.WriteString(stdout, renderDoctorWithNote(status, peers, linksNote, now)); err != nil {
		return 1
	}
	return 0
}

func renderDoctor(s daemon.Status, peers []daemon.Peer, now int64) string {
	return renderDoctorWithNote(s, peers, "– links: not listed without a session; run peerturn doctor --current from the bound session", now)
}

// linksNote explains a nil peer list: no session, or a failed listing.
func renderDoctorWithNote(s daemon.Status, peers []daemon.Peer, linksNote string, now int64) string {
	var b strings.Builder
	fmt.Fprintf(&b, "PeerTurn %s, profile %s, relay %s\n", s.PeerturnVersion, shortLabel(s.ProfileID), orLabel(s.RelayURL, "unknown"))
	relay := "✓ relay reachable, last sync " + agoLabel(s.LastVerifiedAt, now)
	if s.LastError != "" {
		relay = "✗ relay sync failing: " + s.LastError + " (last success " + agoLabel(s.LastVerifiedAt, now) + ")"
	}
	b.WriteString(relay + "\n")
	switch s.Binding {
	case "native_claude", "codex_shared_daemon":
		fmt.Fprintf(&b, "✓ session bound (%s, %s %s)\n", s.Binding, runtimeLabel(s), s.RuntimeVersion)
	case "needs_rebind":
		b.WriteString("✗ session not bound right now (the helper rebinds itself; see below)\n")
	default:
		b.WriteString("– no native session: explicit preview session " + s.Session + "\n")
	}
	switch s.OwnerInvitationHook {
	case "active":
		b.WriteString("✓ prompt hook active: pasted links are seen\n")
	case "":
		b.WriteString("– prompt hook: not applicable\n")
	default:
		b.WriteString("✗ prompt hook " + s.OwnerInvitationHook + "\n")
	}
	if s.CertificateExpiresAt > 0 {
		mark := "✓"
		if s.CertificateExpiresAt <= now {
			mark = "✗"
		}
		fmt.Fprintf(&b, "%s session certificate valid until %s\n", mark, atLabel(s.CertificateExpiresAt))
	}
	switch {
	case peers == nil:
		b.WriteString(linksNote + "\n")
	case len(peers) == 0:
		b.WriteString("– no links yet: peerturn connect prints an invitation\n")
	}
	for _, p := range peers {
		b.WriteString(renderPeer(p, now) + "\n")
	}
	if len(s.InDoubt) > 0 {
		fmt.Fprintf(&b, "✗ %d delivery(ies) in doubt\n", len(s.InDoubt))
	}
	if len(s.Warnings) > 0 {
		b.WriteString("Do next:\n")
		for _, w := range s.Warnings {
			b.WriteString("  • " + w + "\n")
		}
	}
	return b.String()
}

func renderPeer(p daemon.Peer, now int64) string {
	mark := "✓"
	if p.State != "connected" {
		mark = "✗"
	}
	parts := []string{fmt.Sprintf("%s link %s %s", mark, shortLabel(p.Fingerprint), p.State)}
	if p.LinkExpiresAt > 0 {
		parts = append(parts, "renews by use, lapses "+atLabel(p.LinkExpiresAt))
	}
	switch p.Presence {
	case "online", "offline":
		parts = append(parts, fmt.Sprintf("%s (PeerTurn %s, seen %s)", p.Presence, p.PeerVersion, agoLabel(p.LastSeenAt, now)))
	default:
		parts = append(parts, "presence unknown (peer older than 0.2.0 or never heard from)")
	}
	if p.WaitingOnRelay > 0 {
		parts = append(parts, fmt.Sprintf("%d message(s) waiting at the relay since %s", p.WaitingOnRelay, atLabel(p.WaitingSince)))
	}
	return strings.Join(parts, " · ")
}

func runtimeLabel(s daemon.Status) string {
	if s.Binding == "codex_shared_daemon" {
		return "Codex"
	}
	return "Claude Code"
}

func shortLabel(id string) string {
	if len(id) > 12 {
		return id[:12] + "…"
	}
	return id
}

func orLabel(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func atLabel(unix int64) string {
	return time.Unix(unix, 0).Local().Format("2006-01-02 15:04")
}

func agoLabel(unix, now int64) string {
	if unix <= 0 {
		return "never"
	}
	d := now - unix
	switch {
	case d < 90:
		return "just now"
	case d < 90*60:
		return fmt.Sprintf("%d min ago", d/60)
	case d < 36*60*60:
		return fmt.Sprintf("%d h ago", d/3600)
	}
	return fmt.Sprintf("%d d ago", d/86400)
}
