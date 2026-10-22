package localipc

import (
	"strings"
	"testing"
)

func TestInvitationTextBoundaryValidation(t *testing.T) {
	for _, text := range []string{"literal multiline\nowner-pasted prose", strings.Repeat("a", MaxInvitationTextBytes)} {
		if err := (Request{Method: "join.text", InvitationText: text}).Validate(); err != nil {
			t.Fatalf("valid bounded invitation text rejected: %v", err)
		}
	}
	for _, test := range []struct {
		request Request
		code    string
	}{
		{Request{Method: "join.text"}, "invalid_arguments"},
		{Request{Method: "join.text", InvitationText: " \n\t"}, "invalid_arguments"},
		{Request{Method: "join.text", InvitationText: "literal", Link: "other"}, "invalid_arguments"},
		{Request{Method: "send", InvitationText: "literal"}, "invalid_arguments"},
		{Request{Method: "join.text", InvitationText: "literal\x00"}, "invalid_encoding"},
		{Request{Method: "join.text", InvitationText: string([]byte{0xff})}, "invalid_encoding"},
		{Request{Method: "join.text", InvitationText: strings.Repeat("a", MaxInvitationTextBytes+1)}, "too_large"},
		{Request{Method: "join.text", InvitationText: strings.Repeat("я", MaxInvitationTextBytes/2+1)}, "too_large"},
	} {
		err := test.request.Validate()
		if err == nil || PublicError(err).Code != test.code {
			t.Fatalf("invalid invitation text escaped byte/type constraints: %v", err)
		}
	}
}
