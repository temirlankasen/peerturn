package invite

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"testing"

	"github.com/temirlankasen/peerturn/internal/fault"
)

func distinctiveLink(t *testing.T) (Link, [][]byte) {
	t.Helper()
	secrets := [][]byte{bytes.Repeat([]byte("S1z!"), 8), bytes.Repeat([]byte("E9q?"), 8)}
	raw := sampleURL()
	old := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte("s"), 32))
	raw = strings.Replace(raw, "ik="+old, "ik="+base64.RawURLEncoding.EncodeToString(secrets[0]), 1)
	raw = strings.Replace(raw, "ek="+old, "ek="+base64.RawURLEncoding.EncodeToString(secrets[1]), 1)
	link, err := ParseLink(raw)
	if err != nil {
		t.Fatal(err)
	}
	if link.SecretURL() != raw {
		t.Fatal("dedicated secret export changed the capability")
	}
	return link, secrets
}

func assertNoSecrets(t *testing.T, printed string, secrets [][]byte) {
	t.Helper()
	for _, secret := range secrets {
		decimalBytes := make([]string, len(secret))
		for i, value := range secret {
			decimalBytes[i] = strconv.Itoa(int(value))
		}
		decimal := strings.Join(decimalBytes, " ")
		needles := []string{string(secret), decimal, strings.ReplaceAll(decimal, " ", ","),
			hex.EncodeToString(secret), base64.RawURLEncoding.EncodeToString(secret), base64.StdEncoding.EncodeToString(secret)}
		for _, needle := range needles {
			if strings.Contains(printed, needle) {
				t.Fatal("output exposed an invitation secret representation")
			}
		}
	}
}

func TestLinkRedactsNestedFormattingLogsAndErrors(t *testing.T) {
	link, secrets := distinctiveLink(t)
	views := map[string]any{
		"value": link, "pointer": &link,
		"secrets":  link.secrets,
		"exported": struct{ Link Link }{link}, "unexported": struct{ link Link }{link},
		"nested": struct{ value any }{struct{ link Link }{link}},
		"slice":  []Link{link}, "map": map[string]Link{"link": link},
	}
	for name, view := range views {
		t.Run(name, func(t *testing.T) {
			for _, format := range []string{"%v", "%+v", "%#v", "%d", "%s", "%x"} {
				t.Run(format, func(t *testing.T) {
					assertNoSecrets(t, fmt.Sprintf(format, view), secrets)
					assertNoSecrets(t, fmt.Errorf(format, view).Error(), secrets)
				})
			}
			var output bytes.Buffer
			for _, handler := range []slog.Handler{slog.NewTextHandler(&output, nil), slog.NewJSONHandler(&output, nil)} {
				output.Reset()
				slog.New(handler).Info("invitation", "link", view)
				assertNoSecrets(t, output.String(), secrets)
			}
			data, err := json.Marshal(view)
			assertNoSecrets(t, string(data), secrets)
			if err != nil {
				assertNoSecrets(t, err.Error(), secrets)
			}
		})
	}
}

func TestLinkSerializationRequiresDedicatedExport(t *testing.T) {
	link, _ := distinctiveLink(t)
	if _, err := json.Marshal(link); !fault.IsCode(err, fault.InvalidInvite) {
		t.Fatal("generic JSON serialization accepted invitation capability")
	}
	for _, value := range []interface{ MarshalText() ([]byte, error) }{link, link.secrets} {
		if data, err := value.MarshalText(); !fault.IsCode(err, fault.InvalidInvite) || len(data) != 0 {
			t.Fatal("generic text serialization accepted invitation capability")
		}
	}
}
