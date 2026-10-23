package daemon

import (
	"time"

	"github.com/temirlankasen/peerturn/internal/invite"
	"github.com/temirlankasen/peerturn/internal/localipc"
	"github.com/temirlankasen/peerturn/internal/store"
)

const ownerInviteTTL = 10 * time.Minute
const maxPeerInviteHashes = 1024
const maxInviteHashesPerPeer = 128

type ownerInvite struct {
	epoch   string
	expires time.Time
	created time.Time
	handle  string
	host    string
	hint    string
	link    []byte
	timer   *time.Timer
	order   uint64
}

// Pending capabilities are separate from persisted onboarding and peer hashes.
type joinOrigins struct {
	owner        map[string]ownerInvite
	peer         peerInviteCache
	expiredEpoch string
	ownerOrder   uint64
}

func (d *Daemon) recordOwnerInvites(links []string, epoch string) {
	now := time.Now()
	if d.joinOrigins.owner == nil {
		d.joinOrigins.owner = make(map[string]ownerInvite)
	}
	d.expireOwnerInvites(now)
	for hash, record := range d.joinOrigins.owner {
		if record.epoch != epoch {
			d.removeOwnerInvite(hash)
		}
	}
	for _, raw := range links {
		link, err := invite.ParseLink(raw)
		if err != nil {
			continue
		}
		hash, _, err := invite.OriginHashes(raw)
		if err != nil {
			continue
		}
		if _, exists := d.joinOrigins.owner[hash]; exists {
			continue
		}
		if len(d.joinOrigins.owner) >= 8 {
			// A batch shares now, so use insertion order rather than timestamp ties.
			oldest := ""
			for key, record := range d.joinOrigins.owner {
				if oldest == "" || record.order < d.joinOrigins.owner[oldest].order {
					oldest = key
				}
			}
			d.removeOwnerInvite(oldest)
		}
		d.addOwnerInvite(hash, epoch, link, now)
	}
}

func (d *Daemon) recordPeerInvites(peer, text string) {
	for _, hash := range invite.PeerClaimHashes(text) {
		d.joinOrigins.peer.add(peer, hash)
	}
}

func (d *Daemon) recordPeerEntryInvites(item store.InboxEntry) error {
	for id, p := range d.state.Peers {
		g, err := d.incomingGrant(p)
		if err == nil && g.GrantID == item.GrantID {
			d.recordPeerInvites(id, string(item.Body))
			return nil
		}
	}
	return localipc.NewError("revoked", "revoked")
}

func (d *Daemon) checkInvitationOrigin(raw, capability string) error {
	exact, claim, err := invite.OriginHashes(raw)
	if err != nil {
		return err
	}
	if d.joinOrigins.peer.contains(claim) {
		return localipc.NewError("peer_invitation_denied", "rejected")
	}
	if capability == "" {
		return nil
	}
	if err = d.checkJoinCapability(capability); err != nil {
		return err
	}
	binding := d.capabilities[capability]
	d.expireOwnerInvites(time.Now())
	owner, ok := d.joinOrigins.owner[exact]
	if !ok || owner.epoch != binding.nativeEpoch || !time.Now().Before(owner.expires) {
		return d.missingOwnerInvite(binding.nativeEpoch)
	}
	return nil
}
