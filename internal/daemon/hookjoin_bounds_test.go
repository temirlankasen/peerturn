package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/temirlankasen/peerturn/internal/invite"
	"github.com/temirlankasen/peerturn/internal/localipc"
)

func TestOwnerPendingEvictsOldestAndClearsCapability(t *testing.T) {
	parallelDaemonTest(t)
	d, _ := pairedStores(t)
	provenJoinCapability(t, d)
	first := syntheticJoinLink("old.invalid", "A")
	ownerPastedInvite(t, d, first)
	hash, _, err := invite.OriginHashes(first)
	if err != nil {
		t.Fatal(err)
	}
	oldest := d.joinOrigins.owner[hash]
	for i := range 8 {
		ownerPastedInvite(t, d, syntheticJoinLink(fmt.Sprintf("new%d.invalid", i), "A"))
	}
	if _, exists := d.joinOrigins.owner[hash]; exists || len(d.joinOrigins.owner) != 8 {
		t.Fatal("new owner paste did not evict the oldest invitation")
	}
	if strings.Trim(string(oldest.link), "\x00") != "" || oldest.timer.Stop() {
		t.Fatal("eviction retained capability bytes or an active timer")
	}
	newest, _, err := invite.OriginHashes(syntheticJoinLink("new7.invalid", "A"))
	if err != nil || len(d.joinOrigins.owner[newest].link) == 0 {
		t.Fatal("latest owner paste was dropped")
	}
}

func TestOwnerPendingBatchEvictsInInsertionOrder(t *testing.T) {
	parallelDaemonTest(t)
	d := &Daemon{}
	t.Cleanup(d.wipeOwnerInvites)
	var links []string
	for i := range 12 {
		links = append(links, syntheticJoinLink(fmt.Sprintf("batch%d.invalid", i), "A"))
	}
	// A batch shares its timestamp; eviction must still follow insertion order.
	d.recordOwnerInvites(links, "epoch")
	for i, link := range links {
		hash, _, err := invite.OriginHashes(link)
		_, retained := d.joinOrigins.owner[hash]
		if err != nil || retained != (i >= 4) {
			t.Fatal("batch did not retain the latest eight invitations")
		}
	}
}

func TestOwnerPendingShowsRoutingHintWithoutFragment(t *testing.T) {
	parallelDaemonTest(t)
	d, _ := pairedStores(t)
	capability := provenJoinCapability(t, d)
	ownerPastedInvite(t, d, syntheticJoinLink("same.invalid", "A"))
	ownerPastedInvite(t, d, syntheticJoinLink("same.invalid", "B"))
	_, err := d.Dispatch(t.Context(), pendingJoinRequest(t, d, capability, ""))
	raw, encodeErr := json.Marshal(localipc.PublicError(err))
	if encodeErr != nil || !strings.Contains(string(raw), "/i/AAAAAA") || !strings.Contains(string(raw), "/i/BBBBBB") {
		t.Fatal("ambiguous handles lack distinguishing public routing hints")
	}
	for _, secret := range []string{"#v=", "&ik=", "&ek=", "&fp=", "&mh=", strings.Repeat("A", 21)} {
		if strings.Contains(string(raw), secret) {
			t.Fatal("routing hint exposed capability or full routing id")
		}
	}
}

func TestClaudeOwnerBoundaryRecordsBeforeOperationLock(t *testing.T) {
	parallelDaemonTest(t)
	d, _ := pairedStores(t)
	provenJoinCapability(t, d)
	b := d.claudeBinding
	link := syntheticJoinLink("repaste.invalid", "A")
	d.opMu.Lock()
	defer d.opMu.Unlock()
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	_, dispatchErr := d.Dispatch(ctx, localipc.Request{Method: "adapter.boundary", NativeSession: b.NativeSessionID,
		NativeSocket: b.Socket, Instance: b.Instance, OwnerInvites: []string{link}})
	if dispatchErr != nil && !errors.Is(dispatchErr, context.DeadlineExceeded) {
		t.Fatal(dispatchErr)
	}
	hash, _, err := invite.OriginHashes(link)
	if err != nil || len(d.joinOrigins.owner[hash].link) == 0 {
		t.Fatal("operation lock timeout dropped the owner invitation")
	}
}

func TestClaudeOwnerBoundaryBeforeLockRequiresExactLiveBinding(t *testing.T) {
	parallelDaemonTest(t)
	d, _ := pairedStores(t)
	provenJoinCapability(t, d)
	b := d.claudeBinding
	d.opMu.Lock()
	defer d.opMu.Unlock()
	for _, change := range []string{"session", "socket", "instance", "capability", "persistence", "canceled", "expired"} {
		t.Run(change, func(t *testing.T) {
			r := localipc.Request{Method: "adapter.boundary", NativeSession: b.NativeSessionID,
				NativeSocket: b.Socket, Instance: b.Instance, OwnerInvites: []string{syntheticJoinLink("denied.invalid", "A")}}
			ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
			defer cancel()
			switch change {
			case "session":
				r.NativeSession = "foreign"
			case "socket":
				r.NativeSocket = "/foreign.sock"
			case "instance":
				r.Instance = "foreign"
			case "capability":
				r.Capability = "model-capability"
			case "persistence":
				d.persistenceFailed = true
			case "canceled":
				cancel()
			case "expired":
				if err := b.Touch(b.Instance, time.Now().Add(-time.Minute)); err != nil {
					t.Fatal(err)
				}
				// An unrelated instance cannot renew this stale binding first.
				r.Instance = "foreign"
			}
			if _, err := d.Dispatch(ctx, r); err == nil {
				t.Fatal("invalid boundary unexpectedly succeeded")
			}
			if len(d.joinOrigins.owner) != 0 {
				t.Fatal("unproven owner boundary recorded an invitation before the operation lock")
			}
			d.persistenceFailed = false
		})
	}
}
