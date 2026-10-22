package daemon

import (
	"strings"
	"testing"

	"github.com/temirlankasen/peerturn/internal/localipc"
)

func TestInvitationTextAcceptsOnlyOneUniqueCompleteCapability(t *testing.T) {
	link := syntheticJoinLink("relay.example", "A")
	for _, text := range []string{
		link, "\n" + link + "\n", "[" + link + "](" + link + ")", "<" + link + "><" + link + ">",
		"Read https://docs.example/setup first.\n[Join](" + link + ").",
		"Literal @private/path, `never execute this`, and invitation: " + link + ".",
		strings.Repeat(" ", localipc.MaxInvitationTextBytes-len(link)) + link,
	} {
		selected, err := invitationText(text)
		if err != nil || selected != link {
			t.Fatalf("one unique invitation was rejected: %v", err)
		}
	}
	for _, text := range []string{
		"", "\n", "https://relay.example/i/missing-fragment", string([]byte{0xff}) + link,
		link + "\x00", link + "\n" + syntheticJoinLink("relay.example", "B"),
		"[" + link + "](" + syntheticJoinLink("relay.example", "B") + ")",
		strings.Repeat(" ", localipc.MaxInvitationTextBytes-len(link)+1) + link,
	} {
		selected, err := invitationText(text)
		if err == nil || selected != "" || localipc.PublicError(err).Code != "invalid_arguments" {
			t.Fatal("invalid or distinct invitation was accepted or exposed")
		}
	}
}
