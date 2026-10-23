package daemon

import (
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/temirlankasen/peerturn/internal/invite"
	"github.com/temirlankasen/peerturn/internal/localipc"
)

var pastedURL = regexp.MustCompile("https://[^\\s<>\"`()]+")

// Only explicit pasted text is inspected; links are never fetched or expanded.
func invitationText(text string) (string, error) {
	if len(text) > localipc.MaxInvitationTextBytes || !utf8.ValidString(text) || strings.ContainsRune(text, '\x00') {
		return "", localipc.NewError("malformed_invitation", "rejected")
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return "", localipc.NewError("malformed_invitation", "rejected")
	}
	if strings.HasPrefix(text, "https://") && !strings.ContainsAny(text, " \t\r\n") {
		if _, err := invite.ParseLink(text); err != nil {
			return "", localipc.NewError("malformed_invitation", "rejected")
		}
		return text, nil
	}
	var selected string
	for _, candidate := range pastedURL.FindAllString(text, -1) {
		candidate = strings.TrimRight(candidate, ")]},.;'")
		if _, err := invite.ParseLink(candidate); err != nil {
			continue
		}
		if selected != "" && selected != candidate {
			return "", localipc.NewError("malformed_invitation", "rejected")
		}
		selected = candidate
	}
	if selected == "" {
		return "", localipc.NewError("malformed_invitation", "rejected")
	}
	return selected, nil
}
