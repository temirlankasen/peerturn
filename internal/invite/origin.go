package invite

import (
	"crypto/sha256"
	"encoding/hex"
	"net/url"
	"regexp"
	"strings"
)

var candidateURL = regexp.MustCompile("(?i)https://[^\\s<>\"`()]+")
var peerPage = regexp.MustCompile(`(?i)(?:https://)?(?:[a-z0-9][a-z0-9.-]*|\[[a-f0-9:]+\])(?::[0-9]{1,5})?/i/[a-z0-9_-]+`)

// PeerClaimHashes recognizes common transformed links as a best-effort deny
// signal. This never supplies owner evidence or relaxes invitation parsing.
func PeerClaimHashes(text string) []string {
	var hashes []string
	for token := range strings.FieldsSeq(text) {
		// Decode per token so an unrelated malformed escape cannot hide links.
		for range 3 {
			decoded, err := url.PathUnescape(token)
			if err != nil || decoded == token {
				break
			}
			token = decoded
		}
		for _, page := range peerPage.FindAllString(token, -1) {
			if len(page) > MaxLinkBytes {
				continue
			}
			if !strings.HasPrefix(strings.ToLower(page), "https://") {
				page = "https://" + page
			}
			u, err := url.Parse(page)
			if err != nil {
				continue
			}
			u.Scheme, u.Host = "https", strings.ToLower(u.Host)
			u.Path = "/i/" + u.Path[len("/i/"):]
			if hash := ClaimHash(u.String()); hash != "" {
				hashes = append(hashes, hash)
			}
		}
	}
	return hashes
}

// OriginHashes canonicalizes capability fragments without fetching anything.
// The second digest also recognizes the same invitation with changed secrets.
func OriginHashes(raw string) (exact, claim string, err error) {
	link, err := ParseLink(raw)
	if err != nil {
		return "", "", err
	}
	exactHash := sha256.Sum256([]byte(link.SecretURL()))
	claimHash := sha256.Sum256([]byte(link.FetchURL()))
	return hex.EncodeToString(exactHash[:]), hex.EncodeToString(claimHash[:]), nil
}

// ClaimHash recognizes a fragmentless invitation page without accepting queries,
// aliases or a noncanonical routing identifier. It never grants owner authority.
func ClaimHash(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || len(raw) > MaxLinkBytes || u.RawQuery != "" || u.ForceQuery || u.RawPath != "" || u.User != nil || u.Opaque != "" {
		return ""
	}
	id, ok := strings.CutPrefix(u.Path, "/i/")
	host := strings.ToLower(u.Host)
	if !ok || !encodedBytes(id, 16) || !validOrigin(u.Scheme+"://"+host) {
		return ""
	}
	digest := sha256.Sum256([]byte(u.Scheme + "://" + host + u.Path))
	return hex.EncodeToString(digest[:])
}

// PromptLinks exports only complete capabilities explicitly present in an owner
// prompt. Ordinary prose never crosses the hook's private IPC boundary.
func PromptLinks(text string) []string {
	var links []string
	for _, raw := range CandidateURLs(text) {
		link, err := ParseLink(raw)
		if err == nil {
			links = append(links, link.SecretURL())
		}
		if len(links) == 32 {
			break
		}
	}
	return links
}

// PromptHashes is retained for non-secret origin comparison fixtures.
func PromptHashes(text string) []string {
	var hashes []string
	for _, raw := range PromptLinks(text) {
		exact, _, err := OriginHashes(raw)
		if err == nil {
			hashes = append(hashes, exact)
		}
	}
	return hashes
}

// CandidateURLs recognizes link-looking substrings, including fragmentless URLs.
func CandidateURLs(text string) []string {
	var candidates []string
	for _, raw := range candidateURL.FindAllString(text, -1) {
		raw = strings.TrimRight(raw, ")]},.;'")
		if len(raw) <= MaxLinkBytes && strings.Contains(raw, "/i/") {
			candidates = append(candidates, raw)
		}
	}
	return candidates
}
