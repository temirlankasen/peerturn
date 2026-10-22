package auth

import (
	"crypto/ed25519"
	"errors"
	"net/http"

	"github.com/dunglas/httpsfv"
)

var components = []string{"@method", "@authority", "@target-uri", "content-digest"}

type requestProfile struct {
	created, expires int64
	key, nonce       string
	params, digest   string
	signature        []byte
}

func readProfile(r *http.Request) (requestProfile, error) {
	var result requestProfile
	input, err := singleField(r.Header, "Signature-Input", 2048)
	if err != nil {
		return result, err
	}
	member, label, err := dictionaryMember(input, "")
	if err != nil {
		return result, err
	}
	inner, ok := member.(httpsfv.InnerList)
	if !ok || len(inner.Items) != len(components) || inner.Params == nil || len(inner.Params.Names()) != 5 {
		return result, ErrUnauthorized
	}
	for i, item := range inner.Items {
		component, isString := item.Value.(string)
		if !isString || component != components[i] || item.Params == nil || len(item.Params.Names()) != 0 {
			return result, ErrUnauthorized
		}
	}
	created, _ := inner.Params.Get("created")
	expires, _ := inner.Params.Get("expires")
	nonce, _ := inner.Params.Get("nonce")
	kid, _ := inner.Params.Get("keyid")
	algorithm, _ := inner.Params.Get("alg")
	result.created, ok = created.(int64)
	if !ok {
		return result, ErrUnauthorized
	}
	result.expires, ok = expires.(int64)
	if !ok {
		return result, ErrUnauthorized
	}
	result.nonce, ok = nonce.(string)
	if !ok || !validNonce(result.nonce) {
		return result, ErrUnauthorized
	}
	result.key, ok = kid.(string)
	if !ok || !ValidRoutingKey(result.key) || algorithm != "ed25519" {
		return result, ErrUnauthorized
	}
	result.params, err = httpsfv.Marshal(httpsfv.List{inner})
	if err != nil {
		return result, ErrUnauthorized
	}
	result.digest, err = singleField(r.Header, "Content-Digest", 128)
	if err != nil {
		return result, err
	}
	signature, err := singleField(r.Header, "Signature", 256)
	if err != nil {
		return result, err
	}
	member, _, err = dictionaryMember(signature, label)
	if err != nil {
		return result, err
	}
	item, ok := member.(httpsfv.Item)
	if !ok || item.Params == nil || len(item.Params.Names()) != 0 {
		return result, ErrUnauthorized
	}
	result.signature, ok = item.Value.([]byte)
	if !ok || len(result.signature) != ed25519.SignatureSize {
		return result, ErrUnauthorized
	}
	return result, nil
}

func dictionaryMember(raw, requiredLabel string) (httpsfv.Member, string, error) {
	dict, err := httpsfv.UnmarshalDictionary([]string{raw})
	if err != nil || len(dict.Names()) != 1 {
		return nil, "", ErrUnauthorized
	}
	label := dict.Names()[0]
	if requiredLabel != "" && label != requiredLabel {
		return nil, "", ErrUnauthorized
	}
	// Canonical serialization makes duplicate dictionary keys/parameters and
	// multiple textual interpretations invalid in this deliberately strict profile.
	canonical, err := httpsfv.Marshal(dict)
	if err != nil || canonical != raw {
		return nil, "", ErrUnauthorized
	}
	member, ok := dict.Get(label)
	if !ok {
		return nil, "", ErrUnauthorized
	}
	return member, label, nil
}

func singleField(headers http.Header, name string, limit int) (string, error) {
	var values []string
	for key, entries := range headers {
		if http.CanonicalHeaderKey(key) == name {
			values = append(values, entries...)
		}
	}
	if len(values) != 1 || len(values[0]) == 0 || len(values[0]) > limit {
		return "", errors.New("missing or ambiguous authentication field")
	}
	return values[0], nil
}
