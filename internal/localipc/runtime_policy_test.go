package localipc

import (
	"strings"
	"testing"

	"github.com/temirlankasen/peerturn/internal/runtimepolicy"
)

func TestRuntimePolicyFailureSurvivesIPC(t *testing.T) {
	for _, err := range []error{
		runtimepolicy.Check(runtimepolicy.Claude, "private-token"),
		runtimepolicy.Check(runtimepolicy.Codex, "0.162.0"),
		runtimepolicy.ContractFailure(runtimepolicy.Codex, "0.164.0", "queue.list", "probe", "coauthor"),
	} {
		got := RuntimeError(err)
		if got.Code != runtimepolicy.ErrorCode(err) || ExitCode(got) != 6 || !strings.Contains(got.Message, "same") || strings.Contains(got.Message, "private-token") {
			t.Fatalf("lost runtime failure: %+v", got)
		}
	}
}
