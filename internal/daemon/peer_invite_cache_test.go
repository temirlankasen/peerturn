package daemon

import (
	"fmt"
	"testing"

	"github.com/temirlankasen/peerturn/internal/localipc"
)

func TestPeerInviteBucketsIsolateFloodAndRefreshLRU(t *testing.T) {
	var cache peerInviteCache
	cache.add("quiet", "quiet-hash")
	for i := range maxInviteHashesPerPeer {
		cache.add("noisy", fmt.Sprint(i))
	}
	cache.add("noisy", "0")
	cache.add("noisy", "new")
	if !cache.contains("0") || cache.contains("1") || !cache.contains("new") {
		t.Fatal("per-peer eviction did not preserve the most recently used entries")
	}
	for i := range maxPeerInviteHashes * 2 {
		cache.add("noisy", fmt.Sprintf("flood-%d", i))
	}
	if !cache.contains("quiet-hash") || len(cache.buckets["noisy"]) != maxInviteHashesPerPeer || cache.global.Len() != maxInviteHashesPerPeer+1 {
		t.Fatal("one sender consumed another sender's budget")
	}
}

func TestPeerInviteGlobalCapEvictsAndPrunesBuckets(t *testing.T) {
	var cache peerInviteCache
	for i := range maxPeerInviteHashes {
		cache.add(fmt.Sprint(i), fmt.Sprint(i))
	}
	if !cache.contains("0") {
		t.Fatal("oldest entry missing before refresh")
	}
	cache.add("new", "new")
	if cache.contains("1") || !cache.contains("0") || !cache.contains("new") {
		t.Fatal("global eviction refused a new hash or failed LRU ordering")
	}
	if cache.global.Len() != maxPeerInviteHashes || len(cache.buckets) != maxPeerInviteHashes || cache.buckets["1"] != nil {
		t.Fatal("global budget or empty-bucket cleanup failed")
	}
}

func TestEvictedPeerClaimStillNeedsOwnerEvidenceForMCP(t *testing.T) {
	d, _ := pairedStores(t)
	capability := provenJoinCapability(t, d)
	link := syntheticJoinLink("old.invalid", "A")
	d.recordPeerInvites("sender-a", link)
	for i := range maxInviteHashesPerPeer {
		d.recordPeerInvites("sender-a", syntheticJoinLink(fmt.Sprintf("new%d.invalid", i), "A"))
	}
	if err := d.checkInvitationOrigin(link, capability); localipc.PublicError(err).Code != "owner_invitation_required" {
		t.Fatalf("evicted peer link bypassed the owner-hash gate: %v", err)
	}
	ownerPastedInvite(t, d, link)
	if err := d.checkInvitationOrigin(link, capability); err != nil {
		t.Fatalf("peer history blocked fresh owner evidence after eviction: %v", err)
	}
	retained := syntheticJoinLink("new0.invalid", "A")
	ownerPastedInvite(t, d, retained)
	if err := d.checkInvitationOrigin(retained, capability); localipc.PublicError(err).Code != "peer_invitation_denied" {
		t.Fatalf("retained peer claim did not deny owner-pasted copy: %v", err)
	}
}
