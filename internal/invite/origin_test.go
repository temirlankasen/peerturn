package invite

import (
	"net/url"
	"strings"
	"testing"
)

func TestPeerClaimNormalizationDoesNotSupplyOwnerEvidence(t *testing.T) {
	page := "https://relay.example/i/" + strings.Repeat("A", 22)
	want := ClaimHash(page)
	for _, variant := range []string{
		page, strings.TrimPrefix(page, "https://"),
		strings.Replace(page, "https://relay.example/i/", "HTTPS://RELAY.EXAMPLE/I/", 1),
		url.PathEscape(page), url.PathEscape(url.PathEscape(page)),
		strings.ReplaceAll(page, "A", "%41"),
		strings.Replace(page, "/i/", "/%69/", 1),
		"(<[" + page + "]>),.;!", "bad%escape " + page,
		page + "#untrusted-fragment",
	} {
		t.Run(variant, func(t *testing.T) {
			hashes := PeerClaimHashes(variant)
			if len(hashes) != 1 || hashes[0] != want {
				t.Fatal("transformed peer page did not match its canonical routing claim")
			}
			if len(PromptHashes(variant)) != 0 {
				t.Fatal("best-effort peer page normalization supplied owner evidence")
			}
		})
	}
	for _, invalid := range []string{
		page + "A", "https://relay.example/i/short", "https://relay.example/i/%zz",
		"https://relay.example/i/" + strings.Repeat("A", MaxLinkBytes),
	} {
		if len(PeerClaimHashes(invalid)) != 0 {
			t.Fatal("invalid routing identifier consumed peer hash budget")
		}
	}
}
