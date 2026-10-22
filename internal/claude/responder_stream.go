package claude

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/temirlankasen/peerturn/internal/codec"
)

const responderProbe = "PeerTurn local startup check. Reply exactly PEERTURN_PROFILE_OK. Do not use tools."

func (r *Responder) execute(ctx context.Context, cancel context.CancelFunc, request AskRequest) (result AskResult, executionErr error) {
	childSession := newResponderSessionID()
	prepared := false
	defer func() {
		if err := CleanupResponderArtifacts(r.options, request.NativeSessionID, childSession); err != nil {
			result.Answer, executionErr = "", err
			return
		}
		if prepared && r.options.FinishFork != nil {
			finishCtx, finishCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer finishCancel()
			if r.options.FinishFork(finishCtx, childSession) != nil {
				result.Answer, executionErr = "", &ResponderError{Code: ForkCleanupFailed}
			}
		}
	}()
	if r.options.PrepareFork == nil {
		return result, &ResponderError{Code: ProtectedPathPolicyFailed}
	}
	if r.options.PrepareFork(ctx, childSession) != nil {
		return result, &ResponderError{Code: ResponderCancelled}
	}
	prepared = true
	directory, settings, err := responderSettings(r.options)
	if err != nil {
		return result, &ResponderError{Code: ResponderUnavailable}
	}
	defer func() {
		if os.RemoveAll(directory) != nil {
			result.Answer = ""
			executionErr = &ResponderError{Code: ResponderUnavailable}
		}
	}()
	command := responderCommand(ctx, r.options.Executable)
	command.Args = append(command.Args, responderArgs(request.NativeSessionID, settings)...)
	command.Args = append(command.Args, "--resume-session-at", request.Snapshot.leaf,
		"--model", request.Snapshot.Model, "--effort", request.Snapshot.Effort, "--session-id", childSession)
	command.Dir, command.Env, command.Stderr = r.options.WorkingDirectory, responderEnvironment(r.options), io.Discard
	input, err := command.StdinPipe()
	if err != nil {
		return result, &ResponderError{Code: ResponderUnavailable}
	}
	defer func() {
		if closeErr := input.Close(); closeErr != nil && !errors.Is(closeErr, os.ErrClosed) {
			result.Answer = ""
			executionErr = &ResponderError{Code: ResponderFailed}
		}
	}()
	stream := &responderStream{ctx: ctx, cancel: cancel, input: input, request: request, responder: r, result: &result, childSession: childSession}
	command.Stdout = stream
	if !configureResponderProcess(command) || command.Start() != nil {
		return result, &ResponderError{Code: ResponderUnavailable}
	}
	if stream.send(responderProbe) != nil {
		cancel()
	}
	err = command.Wait()
	if stream.failure != nil {
		return result, stream.failure
	}
	if code := responderContextFailure(ctx); code != "" {
		return result, &ResponderError{Code: code}
	}
	if err != nil || !stream.completed || len(stream.buffer) != 0 {
		return result, &ResponderError{Code: ResponderFailed}
	}
	if request.Snapshot.verify(request.NativeSessionID, r.options.WorkingDirectory) != nil ||
		request.Snapshot.verifyFork(result.NativeSessionID) != nil {
		return result, &ResponderError{Code: SnapshotUnavailable}
	}
	if err = VerifyResponderConfiguration(r.options); err != nil {
		return result, err
	}
	answer, count, err := redactAnswer(stream.answer, r.options.KnownSecrets)
	if err != nil {
		return result, &ResponderError{Code: RedactionFailed}
	}
	if len(answer) > r.options.MaxOutputBytes {
		return result, &ResponderError{Code: ResponderBudgetExceeded}
	}
	if err := stream.observe(OutboundRedacted); err != nil {
		return result, err
	}
	result.Answer, result.Redactions = answer, count
	return result, nil
}

type responderStream struct {
	ctx                    context.Context
	cancel                 context.CancelFunc
	input                  io.WriteCloser
	request                AskRequest
	responder              *Responder
	result                 *AskResult
	buffer                 []byte
	total, calls           int
	initialized, peerInput bool
	modelInput, completed  bool
	answer                 string
	childSession           string
	failure                error
}

func (s *responderStream) send(text string) error {
	data, err := json.Marshal(map[string]any{"type": "user", "message": map[string]string{"role": "user", "content": text}})
	if err != nil {
		return err
	}
	_, err = s.input.Write(append(data, '\n'))
	return err
}

func (s *responderStream) observe(stage ResponderStage) error {
	if s.responder.options.Observer != nil {
		if err := s.responder.options.Observer(s.ctx, stage, s.result.NativeSessionID); err != nil {
			return &ResponderError{Code: ResponderCancelled}
		}
	}
	s.result.Stages = append(s.result.Stages, stage)
	return nil
}

func (s *responderStream) Write(data []byte) (int, error) {
	n := len(data)
	s.total += n
	if s.total > 16<<20 || len(s.buffer)+n > 1<<20 {
		return s.reject(ResponderBudgetExceeded)
	}
	s.buffer = append(s.buffer, data...)
	for {
		index := bytes.IndexByte(s.buffer, '\n')
		if index < 0 {
			return n, nil
		}
		line := s.buffer[:index]
		if err := s.event(line); err != nil {
			s.failure = err
			s.cancel()
			return 0, err
		}
		s.buffer = s.buffer[index+1:]
	}
}

func (s *responderStream) reject(code ResponderFailure) (int, error) {
	s.failure = &ResponderError{Code: code}
	s.cancel()
	return 0, s.failure
}

func (s *responderStream) event(line []byte) error {
	if s.completed || codec.Validate(line, 1<<20) != nil {
		return &ResponderError{Code: ResponderFailed}
	}
	var event struct {
		Type, Subtype string
		SessionID     string          `json:"session_id"`
		Error         json.RawMessage `json:"error"`
		Message       struct {
			Role, Model string
			Content     []struct{ Type, Name string }
		} `json:"message"`
	}
	if json.Unmarshal(line, &event) != nil {
		return &ResponderError{Code: ResponderFailed}
	}
	if event.Type == "system" {
		if s.initialized && slices.Contains([]string{"thinking_tokens", "api_retry"}, event.Subtype) {
			return nil
		}
		if event.Subtype != "init" {
			return &ResponderError{Code: ProfileMismatch}
		}
		child, err := verifyResponderInit(line, s.request.Snapshot.Model, s.request.NativeSessionID)
		if err != nil {
			return err
		}
		if child != s.childSession {
			return &ResponderError{Code: ProfileMismatch}
		}
		if s.initialized {
			if child != s.result.NativeSessionID {
				return &ResponderError{Code: ProfileMismatch}
			}
			return nil
		}
		s.result.NativeSessionID, s.initialized = child, true
		return s.observe(ResponderStarted)
	}
	if !s.initialized {
		return &ResponderError{Code: ProfileMismatch}
	}
	switch event.Type {
	case "assistant":
		if (len(event.Error) != 0 && string(event.Error) != "null") ||
			event.SessionID != s.result.NativeSessionID || event.Message.Role != "assistant" ||
			len(event.Message.Content) == 0 {
			return &ResponderError{Code: ResponderFailed}
		}
		if event.Message.Model != s.request.Snapshot.effectiveModel {
			return &ResponderError{Code: ModelMismatch}
		}
		for _, block := range event.Message.Content {
			if block.Type == "tool_use" {
				s.calls++
				if !s.peerInput || !slices.Contains([]string{"Read", "Grep", "Glob"}, block.Name) || s.calls > 8 {
					return &ResponderError{Code: ProfileMismatch}
				}
			}
		}
		if s.peerInput && !s.modelInput {
			s.modelInput = true
			return s.observe(ResponderModelInput)
		}
	case "result":
		if event.SessionID != s.result.NativeSessionID {
			return &ResponderError{Code: ResponderFailed}
		}
		answer, err := decodeResponderAnswer(line, s.request.NativeSessionID, 1<<20)
		if err != nil {
			return err
		}
		if !s.peerInput {
			if strings.TrimSpace(answer) != "PEERTURN_PROFILE_OK" ||
				s.request.Snapshot.verify(s.request.NativeSessionID, s.responder.options.WorkingDirectory) != nil {
				return &ResponderError{Code: SnapshotUnavailable}
			}
			if err := s.request.Snapshot.verifyFork(s.result.NativeSessionID); err != nil {
				return err
			}
			if s.responder.options.AdmitPeerInput == nil {
				return &ResponderError{Code: ProtectedPathPolicyFailed}
			}
			if err := s.responder.options.AdmitPeerInput(s.ctx, s.result.NativeSessionID, func() error {
				if code := responderContextFailure(s.ctx); code != "" {
					return &ResponderError{Code: code}
				}
				if err := VerifyResponderConfiguration(s.responder.options); err != nil {
					return err
				}
				return s.send(s.request.FramedQuestion)
			}); err != nil {
				return &ResponderError{Code: ResponderCancelled}
			}
			s.peerInput = true
			return nil
		}
		if !s.modelInput || len(answer) > s.responder.options.MaxOutputBytes {
			return &ResponderError{Code: ResponderBudgetExceeded}
		}
		s.answer, s.completed = answer, true
		if err := s.input.Close(); err != nil {
			return &ResponderError{Code: ResponderFailed}
		}
		return s.observe(ResponderCompleted)
	case "user", "rate_limit_event":
		return nil
	default:
		return &ResponderError{Code: ProfileMismatch}
	}
	return nil
}

func verifyResponderInit(data []byte, model, parent string) (string, error) {
	failure := &ResponderError{Code: ProfileMismatch}
	if codec.Validate(data, 1<<20) != nil {
		return "", failure
	}
	var init struct {
		Type, Subtype, Model, PermissionMode string
		Session                              string `json:"session_id"`
		Tools                                []string
		MCP                                  *[]json.RawMessage `json:"mcp_servers"`
		Plugins, Skills                      *[]json.RawMessage
	}
	if json.Unmarshal(data, &init) != nil || init.Type != "system" || init.Subtype != "init" ||
		init.Model != model || init.PermissionMode != "default" || !validNativeSessionID(init.Session) || init.Session == parent ||
		len(init.Tools) != 3 || init.MCP == nil || len(*init.MCP) != 0 || init.Plugins == nil || len(*init.Plugins) != 0 ||
		init.Skills == nil || len(*init.Skills) != 0 {
		return "", failure
	}
	slices.Sort(init.Tools)
	if !slices.Equal(init.Tools, []string{"Glob", "Grep", "Read"}) {
		return "", failure
	}
	return init.Session, nil
}
