package daemon

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/temirlankasen/peerturn/internal/identity"
	"github.com/temirlankasen/peerturn/internal/runtimepolicy"
)

// peerList adds custody facts to the journal's view of each link: when the
// grant lapses, when the peer's daemon was last heard from, and how many own
// messages it has not fetched yet.
func (d *Daemon) peerList(ctx context.Context) ([]Peer, error) {
	now := unixNow()
	peers := d.peers()
	for i := range peers {
		p := d.state.Peers[peers[i].ID]
		if g, err := d.incomingGrant(p); err == nil {
			peers[i].LinkExpiresAt = g.ExpiresAt
		}
		peers[i].LastSeenAt, peers[i].PeerVersion, peers[i].Presence = p.LastSeenAt, p.PeerVersion, presenceOf(p, now)
		cert, err := identity.VerifyChain(p.Chain, identity.RootFingerprint(p.Chain.RootPublicKey), now)
		if err != nil {
			continue
		}
		count, since, err := d.db.OutboundWaiting(ctx, d.cert.Binding, cert.Binding, now)
		if err != nil {
			return nil, err
		}
		peers[i].WaitingOnRelay, peers[i].WaitingSince = count, since
	}
	return peers, nil
}

// explainStatus turns raw fields into the owner's questions: what is bound,
// what the hook sees, what expires, what waits, and what to do about it.
func (d *Daemon) explainStatus(ctx context.Context, s *Status) error {
	now := unixNow()
	s.RelayURL, s.LatestVersion = d.state.RelayURL, d.latestVersion
	s.CertificateNotBefore, s.CertificateExpiresAt = d.cert.NotBefore, d.cert.ExpiresAt
	s.OwnerInvitationHook = d.hookState()
	ids, err := d.db.UncertainDeliveryIDs(ctx)
	if err != nil {
		return err
	}
	s.InDoubt = ids
	peers, err := d.peerList(ctx)
	if err != nil {
		return err
	}
	s.Warnings = statusWarnings(*s, peers, now)
	s.Summary = statusSummary(*s, peers)
	return nil
}

// hookState says whether a pasted link would be seen right now. Only the
// mod's prompt report proves the hook; a bound helper alone does not.
func (d *Daemon) hookState() string {
	b := d.claudeBinding
	switch {
	case d.state.Codex != nil:
		if d.hookVerified > 0 {
			return "active"
		}
		return "not_observed"
	case d.state.NativeSessionID == "":
		return ""
	case !b.Ready(time.Now()):
		return "unbound"
	case !b.CapabilityAvailable("owner_invitation"):
		return "disabled"
	case b.LastBoundaryAt.IsZero():
		return "not_observed"
	}
	return "active"
}

func statusWarnings(s Status, peers []Peer, now int64) []string {
	w := []string{}
	if s.Binding == "needs_rebind" {
		w = append(w, "Session not bound: the plugin helper rebinds by itself within seconds; if peer tools still fail after a minute, run peerturn session bind --current from this session.")
	}
	if s.RuntimePolicy != nil && s.RuntimePolicy.Status == runtimepolicy.NewerUnverified {
		w = append(w, fmt.Sprintf("%s %s is newer than the last verified release; PeerTurn works on it but has not been re-verified there.", runtimeName(s.RuntimePolicy.Runtime), s.RuntimeVersion))
	}
	switch s.OwnerInvitationHook {
	case "not_observed":
		w = append(w, "The prompt hook has not reported an owner message in this binding yet: a link pasted before the helper was ready was not seen. Paste it again, or use peerturn join --current --link-stdin.")
	case "disabled":
		w = append(w, "The prompt hook is disabled for this session (host contract failed); accept links with peerturn join --current --link-stdin.")
	}
	if len(s.InDoubt) > 0 {
		ids := make([]string, 0, len(s.InDoubt))
		for _, id := range s.InDoubt {
			ids = append(ids, shortID(id))
		}
		w = append(w, fmt.Sprintf("%d delivery(ies) in doubt (%s): decide with peerturn delivery resolve ID --sent or --not-sent; nothing is resent automatically.", len(ids), strings.Join(ids, ", ")))
	}
	if s.LastError != "" {
		w = append(w, "Relay sync is failing with "+s.LastError+"; last success "+ago(s.LastVerifiedAt, now)+".")
	}
	if s.CertificateExpiresAt > 0 && s.CertificateExpiresAt-now < 30*24*60*60 {
		w = append(w, "The session certificate expires "+at(s.CertificateExpiresAt)+"; it renews itself at the next daemon start or invitation.")
	}
	for _, p := range peers {
		w = append(w, peerWarnings(p, now)...)
	}
	if s.LatestVersion != "" && s.LatestVersion != s.PeerturnVersion {
		w = append(w, "PeerTurn "+s.LatestVersion+" is available: run peerturn upgrade.")
	}
	return w
}

func peerWarnings(p Peer, now int64) []string {
	w := []string{}
	name := shortID(p.Fingerprint)
	switch p.State {
	case "expired":
		w = append(w, "Link with "+name+" expired: a new invitation is needed.")
	case "waiting_inviter":
		w = append(w, "Link with "+name+" waits for the inviter's daemon to confirm; it completes when that daemon fetches its mail.")
	case "revoked":
		w = append(w, "Link with "+name+" was revoked; a new invitation restores it.")
	}
	if p.WaitingOnRelay > 0 && now-p.WaitingSince > 15*60 {
		seen := "never heard from it (PeerTurn older than 0.2.0, or not running)"
		if p.LastSeenAt > 0 {
			seen = "last heard from it " + ago(p.LastSeenAt, now)
		}
		w = append(w, fmt.Sprintf("%d message(s) to %s wait at the relay since %s; its daemon has not fetched them, %s.", p.WaitingOnRelay, name, at(p.WaitingSince), seen))
	}
	if p.State == "connected" && p.LinkExpiresAt > 0 && p.LinkExpiresAt-now < 3*24*60*60 {
		w = append(w, "Link with "+name+" lapses "+at(p.LinkExpiresAt)+" unless used; any message renews it.")
	}
	return w
}

func statusSummary(s Status, peers []Peer) string {
	binding := "no session bound"
	switch s.Binding {
	case "native_claude":
		binding = "bound to this Claude session"
	case "codex_shared_daemon":
		binding = "bound to this Codex thread"
	case "needs_rebind":
		binding = "session binding being restored"
	case "test_session":
		binding = "explicit preview session"
	}
	relay := "relay ok"
	if s.LastError != "" {
		relay = "relay failing (" + s.LastError + ")"
	}
	connected, waiting := 0, 0
	for _, p := range peers {
		if p.State == "connected" {
			connected++
		}
		waiting += p.WaitingOnRelay
	}
	links := fmt.Sprintf("%d link(s), %d connected", len(peers), connected)
	if waiting > 0 {
		links += fmt.Sprintf(", %d message(s) waiting at the relay", waiting)
	}
	return strings.Join([]string{binding, "hook " + orWord(s.OwnerInvitationHook, "n/a"), relay, links}, "; ")
}

func runtimeName(runtime string) string {
	if runtime == runtimepolicy.Codex {
		return "Codex"
	}
	return "Claude Code"
}

func orWord(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func at(unix int64) string {
	return time.Unix(unix, 0).Local().Format("2006-01-02 15:04")
}

func ago(unix, now int64) string {
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
