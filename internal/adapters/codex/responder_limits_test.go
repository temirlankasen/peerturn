package codex

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestResponderOnlyReturnsWholeRedactedFinalAnswer(t *testing.T) {
	cfg := qualifiedFixture()
	cfg.Secrets = []KnownSecret{{Class: "authority", Value: "fixture-sensitive-value"}}
	cfg.run = func(context.Context, ResponderCommand, int) ([]byte, error) {
		raw := completedFixture("Answer fixture-sensitive-value")
		raw = strings.Replace(raw, `{"type":"turn.started"}`+"\n", `{"type":"turn.started"}`+"\n"+
			`{"type":"item.completed","item":{"id":"reasoning","type":"reasoning","text":"private chain"}}`+"\n"+
			`{"type":"item.completed","item":{"id":"tool","type":"command_execution","aggregated_output":"private tool"}}`+"\n", 1)
		return []byte(raw), nil
	}
	r, err := NewResponder(cfg)
	if err != nil {
		t.Fatal(err)
	}
	got, err := r.Respond(t.Context(), responderFixture())
	if err != nil || got.Answer != "Answer [REDACTED:authority]" || got.Redactions != 1 || got.Usage.InputTokens != 12 {
		t.Fatalf("result %+v error %v", got, err)
	}
}

func TestResponderDeadlineNeverReleasesPartialAnswer(t *testing.T) {
	cfg := qualifiedFixture()
	cfg.Deadline = time.Millisecond
	cfg.run = func(ctx context.Context, _ ResponderCommand, _ int) ([]byte, error) {
		<-ctx.Done()
		return []byte(completedFixture("late answer")), ctx.Err()
	}
	r, err := NewResponder(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := r.Respond(t.Context(), responderFixture()); err == nil || got.Answer != "" {
		t.Fatalf("result %+v error %v", got, err)
	}
}

func TestResponderSerializesParentAndConversationAndBoundsDaemon(t *testing.T) {
	for _, mode := range []string{"parent", "conversation", "daemon"} {
		t.Run(mode, func(t *testing.T) {
			cfg := qualifiedFixture()
			if mode == "daemon" {
				cfg.MaxConcurrent = 1
			}
			started, release := make(chan struct{}), make(chan struct{})
			cfg.run = func(context.Context, ResponderCommand, int) ([]byte, error) {
				close(started)
				<-release
				return []byte(completedFixture("answer")), nil
			}
			r, err := NewResponder(cfg)
			if err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() { _, runErr := r.Respond(t.Context(), responderFixture()); done <- runErr }()
			<-started
			request := responderFixture()
			request.MessageID = "other-message"
			request.AttemptID = "other-attempt"
			switch mode {
			case "parent":
				request.ConversationID = "other-conversation"
			case "conversation":
				request.ParentThreadID = "01a15e8a-84b2-7372-92ae-3a13945766f8"
			case "daemon":
				request.ParentThreadID = "01a15e8a-84b2-7372-92ae-3a13945766f8"
				request.ConversationID = "other-conversation"
			}
			_, err = r.Respond(t.Context(), request)
			close(release)
			if runErr := <-done; runErr != nil {
				t.Fatal(runErr)
			}
			if responderReason(err) != "budget_exceeded" {
				t.Fatal(err)
			}
		})
	}
}

func TestResponderCaptureCombinesOutputAndDiagnosticsBudget(t *testing.T) {
	cancelled := false
	capture := &responderCapture{limit: 10, cancel: func() { cancelled = true }}
	discard := &responderDiscard{capture: capture}
	if _, err := discard.Write([]byte("123456")); err != nil {
		t.Fatal(err)
	}
	if _, err := capture.Write([]byte("12345")); err == nil || !capture.overflow || !cancelled || capture.buf.Len() != 0 {
		t.Fatal("combined capture limit was bypassed")
	}
}

func TestResponderBudgetFailureBlocksSpawnAndSettlementBlocksReply(t *testing.T) {
	for _, stage := range []string{"reserve", "finish"} {
		t.Run(stage, func(t *testing.T) {
			cfg := qualifiedFixture()
			cfg.Budget = callbackBudget{fail: stage}
			cfg.run = func(context.Context, ResponderCommand, int) ([]byte, error) {
				if stage == "reserve" {
					t.Error("spawn before successful reservation")
				}
				return []byte(completedFixture("answer")), nil
			}
			r, err := NewResponder(cfg)
			if err != nil {
				t.Fatal(err)
			}
			if got, err := r.Respond(t.Context(), responderFixture()); err == nil || got.Answer != "" {
				t.Fatalf("result %+v error %v", got, err)
			}
		})
	}
}

type callbackBudget struct{ fail string }

func (b callbackBudget) Reserve(context.Context, ResponderRequest, ResponderLimits) (BudgetReservation, error) {
	if b.fail == "reserve" {
		return nil, errors.New("private billing data")
	}
	return b, nil
}
func (b callbackBudget) Finish(context.Context, ResponderUsage, bool) error {
	if b.fail == "finish" {
		return errors.New("private billing data")
	}
	return nil
}

func TestResponderOutputRejectsUnknownUsageAndInheritedEffectfulTools(t *testing.T) {
	for _, output := range []string{
		strings.Replace(completedFixture("answer"), `"input_tokens":12,"cached_input_tokens":0,"output_tokens":3`, "", 1),
		strings.Replace(completedFixture("answer"), `"type":"agent_message"`, `"type":"mcp_tool_call"`, 1),
		completedFixture("answer") + `{"type":"error","message":"private diagnostic"}` + "\n",
	} {
		if got, err := parseResponderOutput([]byte(output), responderFixture().ParentThreadID, ResponderLimits{OutputBytes: 16384, ToolCalls: 8}); err == nil || got.Answer != "" {
			t.Fatalf("result %+v error %v", got, err)
		}
	}
}

func TestResponderRejectsAuthorityEnvironmentAndOpaqueDiagnosticReason(t *testing.T) {
	for _, environment := range [][]string{{"OPENAI_API_KEY=fixture"}, {"PEERTURN_SOCKET=/private/socket"}, {"PATH=/attacker/bin"}} {
		if _, err := BuildResponderCommand("/opt/codex", "/work", environment, responderFixture()); responderReason(err) != "protected_path_policy_failed" {
			t.Fatal(err)
		}
	}
	if got := safeResponderError(&ResponderError{Reason: "secret diagnostic"}); got.Error() != "responder_failed" {
		t.Fatal(got)
	}
}
