package daemon

import (
	"context"
	"strings"
	"unicode"

	"github.com/temirlankasen/peerturn/internal/relayclient"
	"github.com/temirlankasen/peerturn/internal/version"
)

// A heartbeat is a tiny link update carrying only a version. It lets the
// owner see whether the peer's daemon fetches its mail at all, which is the
// first question when a message sits at the relay. A peer older than 0.2.0
// rejects the unknown fields as one skipped mailbox slot and is unharmed.
const (
	presenceInterval int64 = 10 * 60
	presenceTTL      int64 = 15 * 60
)

// presenceOf is unknown for peers that never sent a heartbeat: silence from
// an older PeerTurn says nothing about whether its daemon runs.
func presenceOf(p *peerState, now int64) string {
	if p.PeerVersion == "" || p.LastSeenAt == 0 {
		return "unknown"
	}
	if now-p.LastSeenAt <= 2*presenceInterval+60 {
		return "online"
	}
	return "offline"
}

// markSeen records any verified packet from the peer. State is written only
// when the mark moved by a minute, so a burst of packets is not a burst of
// sealed journal writes.
func (d *Daemon) markSeen(p *peerState) error {
	now := unixNow()
	previous := p.LastSeenAt
	p.LastSeenAt = now
	if now-previous < 60 {
		return nil
	}
	return d.save()
}

func peerVersionLabel(raw string) string {
	raw = strings.TrimSpace(raw)
	if len(raw) > 32 {
		return ""
	}
	for _, r := range raw {
		if r > unicode.MaxASCII || !unicode.IsPrint(r) || unicode.IsSpace(r) {
			return ""
		}
	}
	return raw
}

// syncPresence sends one heartbeat per connected peer per interval. Nothing
// is persisted: after a restart the first tick tells every peer "back".
func (d *Daemon) syncPresence(ctx context.Context) error {
	now := unixNow()
	for id, p := range d.state.Peers {
		if !p.connected() || now-d.heartbeatSent[id] < presenceInterval {
			continue
		}
		packet, err := d.handshakePacketTTL(p, handshake{Heartbeat: true, Version: version.Version}, presenceTTL)
		if err != nil {
			continue
		}
		_, err = networkCall(ctx, d, func() (relayclient.Receipt, error) { return d.relay.Send(ctx, packet) })
		if retryCustody(err) {
			return err
		}
		if err == nil {
			d.heartbeatSent[id] = now
		}
	}
	return nil
}

// acceptHeartbeat records the peer's version; the packet itself already
// moved LastSeenAt. A heartbeat must not smuggle a grant or a floor.
func (d *Daemon) acceptHeartbeat(p *peerState, u handshake) error {
	if u.Grant != "" || u.Floor != 0 {
		return invalidHandshake()
	}
	if label := peerVersionLabel(u.Version); label != "" && label != p.PeerVersion {
		p.PeerVersion = label
		return d.save()
	}
	return nil
}
