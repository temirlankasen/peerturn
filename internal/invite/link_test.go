package invite

import (
	"encoding/base64"
	"fmt"
	"strings"
	"testing"

	"github.com/temirlankasen/peerturn/internal/fault"
)

func sampleURL() string {
	key := base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat("s", 32)))
	id := base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat("i", 16)))
	return "https://relay.example/i/" + id + "#v=1&ik=" + key + "&ek=" + key + "&fp=" + strings.Repeat("a", 64) + "&mh=" + strings.Repeat("b", 64)
}

func TestLinkRoundTripAndRedaction(t *testing.T) {
	raw := sampleURL()
	link, err := ParseLink(raw)
	if err != nil {
		t.Fatal(err)
	}
	if link.SecretURL() != raw || strings.Contains(link.FetchURL(), "#") || strings.Contains(link.FetchURL(), "ik=") {
		t.Fatal("URL round trip or fragment stripping failed")
	}
	for _, printed := range []string{fmt.Sprint(link), fmt.Sprintf("%+v", link), fmt.Sprintf("%#v", link)} {
		assertNoSecrets(t, printed, [][]byte{[]byte(strings.Repeat("s", 32))})
		if strings.Contains(printed, "ik=") || strings.Contains(printed, "ek=") {
			t.Fatal("formatting exposed invitation keys")
		}
	}
}

func TestLinkRejectsAmbiguousOrUnsafeInput(t *testing.T) {
	raw := sampleURL()
	inputs := []struct {
		raw  string
		code fault.Code
	}{
		{strings.Replace(raw, "https:", "http:", 1), fault.InvalidInvite},
		{strings.Replace(raw, "relay.example", "user:secret"+"@"+"relay.example", 1), fault.InvalidInvite},
		{strings.Replace(raw, "#", "?ik=secret#", 1), fault.InvalidInvite},
		{raw + "&ik=secret", fault.InvalidInvite}, {raw + "&extra=secret", fault.InvalidInvite},
		{strings.Replace(raw, "v=1", "v=2", 1), fault.UnsupportedVersion},
		{strings.Replace(raw, "v=1", "v=%31", 1), fault.InvalidInvite},
		{strings.Replace(raw, "/i/", "/i/../", 1), fault.InvalidInvite},
		{strings.Replace(raw, "/i/", "/i/%61", 1), fault.InvalidInvite},
		{strings.Replace(raw, "&ek=", "&ek=+", 1), fault.InvalidInvite},
		{strings.Replace(raw, "&fp=", "&fp=A", 1), fault.InvalidInvite},
		{"https://%zz/secret#ik=sensitive", fault.InvalidInvite},
		{strings.Repeat("sensitive", MaxLinkBytes), fault.TooLarge},
	}
	for i, input := range inputs {
		_, err := ParseLink(input.raw)
		if !fault.IsCode(err, input.code) {
			t.Fatalf("case %d: expected %s, got %v", i, input.code, err)
		}
		if strings.Contains(err.Error(), "sensitive") || strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), input.raw) {
			t.Fatalf("case %d exposed input", i)
		}
	}
	_, err := ParseLink(strings.Repeat("x", MaxLinkBytes+1))
	if !fault.IsCode(err, fault.TooLarge) {
		t.Fatal("missing length bound")
	}
}

func FuzzParseLink(f *testing.F) {
	f.Add(sampleURL())
	f.Add("https://relay.example/i/id#secret")
	f.Fuzz(func(t *testing.T, raw string) {
		link, err := ParseLink(raw)
		if err == nil {
			again, err := ParseLink(link.SecretURL())
			if err != nil || again.FetchURL() != link.FetchURL() || strings.Contains(link.FetchURL(), "#") {
				t.Fatal("accepted link failed canonical round trip")
			}
		}
	})
}
