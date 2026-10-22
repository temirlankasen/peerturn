package main

import (
	"io"

	"github.com/temirlankasen/peerturn/internal/localipc"
)

// Pasted invitation parsing stays in the daemon; the CLI only bounds stdin and
// constructs the private IPC request without a secret in process arguments.
func invitationRequest(reader io.Reader, request localipc.Request) (localipc.Request, error) {
	text, err := readBounded(reader, localipc.MaxInvitationTextBytes)
	if err != nil {
		return request, invalidArguments()
	}
	request.Method, request.InvitationText = "join.text", text
	return request, request.Validate()
}
