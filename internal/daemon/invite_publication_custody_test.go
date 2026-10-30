package daemon

import (
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/temirlankasen/peerturn/internal/localipc"
	"github.com/temirlankasen/peerturn/internal/protocol"
)

func TestInvitePublicationRealQuotaBackoffKeepsPeerFetchAndACK(t *testing.T) {
	parallelDaemonTest(t)
	f := newSelfServiceE2ERelay(t, publicKey(routingKey(t)[32:]), true)
	next := f.server.Config.Handler
	var mu sync.Mutex
	posts := map[string]int{}
	f.server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" && strings.HasPrefix(r.URL.Path, "/v1/invites/") && !strings.HasSuffix(r.URL.Path, "/claims") {
			mu.Lock()
			posts[r.URL.Path]++
			mu.Unlock()
		}
		next.ServeHTTP(w, r)
	})
	a := openSelfServiceE2EDaemon(t, f, "synthetic-quota-author")
	b := openSelfServiceE2EDaemon(t, f, "synthetic-quota-peer")
	if err := requestSelfServiceInvite(t, a); err != nil {
		t.Fatal("initial publication failed:", safeCode(err))
	}
	entry := publicationEntry(t, a)
	if _, err := b.join(t.Context(), entry.URL); err != nil {
		t.Fatal("normal synthetic join failed:", safeCode(err))
	}
	selfServiceE2ESyncUntil(t, a, b, func() bool {
		ap, bp := a.peers(), b.peers()
		return len(ap) == 1 && len(bp) == 1 && ap[0].State == "connected" && bp[0].State == "connected"
	})
	for range 3 {
		if err := requestSelfServiceInvite(t, a); err != nil {
			t.Fatal("publication below the default active-invite quota failed:", safeCode(err))
		}
	}
	if err := requestSelfServiceInvite(t, a); safeCode(err) != "quota" {
		t.Fatal("fifth active invitation did not hit the real default quota:", safeCode(err))
	}
	var failed *issuedInvite
	for _, pending := range a.state.Issued {
		if pending.Failure == "quota" {
			failed = pending
		}
	}
	if failed == nil {
		t.Fatal("real quota refusal was not retained diagnostically")
	}
	failed.publication.retryAt = time.Now().Add(time.Minute)
	path := "/v1/invites/" + failed.Descriptor.InviteID
	mu.Lock()
	before := posts[path]
	mu.Unlock()
	const body = "synthetic peer custody while an unrelated invite is deferred"
	var receipt protocol.ReceiptPayload
	selfServiceE2ECall(t, b, localipc.Request{Method: "send", Peer: peerID(a.state.Chain), Kind: "coauthor", Body: body}, &receipt)
	if receipt.Stage != "relay_stored" {
		t.Fatal("peer data did not reach real encrypted relay custody")
	}
	if err := a.Sync(t.Context()); err != nil {
		t.Fatal("deferred quota refusal starved public Sync:", safeCode(err))
	}
	var inbox InboxPage
	selfServiceE2ECall(t, a, localipc.Request{Method: "inbox"}, &inbox)
	count := 0
	for _, item := range inbox.Messages {
		if item.MessageID == receipt.MessageID && item.BodyUTF8 == body && item.Stage == "receiver_stored" {
			count++
		}
	}
	var acked bool
	err := f.db.QueryRowContext(t.Context(), "SELECT packet IS NULL FROM messages WHERE mailbox_id=? AND message_id=?", a.mailbox(), receipt.MessageID).Scan(&acked)
	if err != nil || !acked || count != 1 {
		t.Fatal("publication backoff prevented peer Fetch/custody/ACK")
	}
	mu.Lock()
	after := posts[path]
	mu.Unlock()
	status := statusFor(a.state, "", 0, "")
	if before != 1 || after != before || len(status.OnboardingErrors) != 1 || status.OnboardingErrors[0] != "quota" {
		t.Fatal("deferred quota repeated POST or lost its truthful diagnostics")
	}
	assertSelfServiceE2ECounts(t, f, selfServiceE2ECounts{inviters: 1, mailboxes: 2, invites: 4, permits: 2})
}
