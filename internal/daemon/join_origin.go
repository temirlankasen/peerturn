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
}

// Only bounded, best-effort peer claim hashes are retained. Owner prompt
// evidence is checked independently, including after peer hashes are evicted.
type joinOrigins struct {
	owner map[string]ownerInvite
	peer  peerInviteCache
}

func (d *Daemon) recordOwnerInvites(hashes []string, epoch string) {
	now := time.Now()
	if d.joinOrigins.owner == nil {
		d.joinOrigins.owner = make(map[string]ownerInvite)
	}
	for hash, record := range d.joinOrigins.owner {
		if !now.Before(record.expires) || record.epoch != epoch {
			delete(d.joinOrigins.owner, hash)
		}
	}
	for _, hash := range hashes {
		if len(d.joinOrigins.owner) >= 128 {
			break
		}
		d.joinOrigins.owner[hash] = ownerInvite{epoch: epoch, expires: now.Add(ownerInviteTTL)}
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
	owner, ok := d.joinOrigins.owner[exact]
	if !ok || owner.epoch != binding.nativeEpoch || !time.Now().Before(owner.expires) {
		return localipc.NewError("owner_invitation_required", "rejected")
	}
	return nil
}
