package agent

import (
	"context"
	"fmt"
	"go-coding-agent/internal/tools"
	"go-coding-agent/internal/trace"
	"io"
	"time"
)

const defaultMaxTurns = 20

type Agent struct {
	root                string
	tools               []tools.Tool
	model               Model
	instructions        string
	maxTurns            int
	messages            []Message
	tokenUsage          int
	traceWriter         *trace.Writer
	sessionID           string
	workspaceTools      *tools.WorkspaceTools
	hasUnverifiedChange bool
	lastVerification    *verificationFact
}

// EnableTrace enables JSONL tracing for the agent session.
func (agent *Agent) EnableTrace(writer trace.Writer) error {
	if writer.Path == "" {
		return fmt.Errorf("enable trace: path is empty")
	}
	sessionID, err := newTraceID("session")
	if err != nil {
		return fmt.Errorf("enable trace: %w", err)
	}
	agent.traceWriter = &writer
	agent.sessionID = sessionID
	return nil
}

type RunResult struct {
	Content string
}

// NewAgent creates an agent with workspace tools.
func NewAgent(root string, model Model, instructions string, maxTurns ...int) (*Agent, error) {
	if root == "" {
		return nil, fmt.Errorf("new agent: root is empty")
	}
	if model == nil {
		return nil, fmt.Errorf("new agent: model is nil")
	}
	if len(maxTurns) > 1 {
		return nil, fmt.Errorf("new agent: max turns accepts at most one value")
	}
	turns := defaultMaxTurns
	if len(maxTurns) == 1 {
		if maxTurns[0] <= 0 {
			return nil, fmt.Errorf("new agent: max turns must be positive")
		}
		turns = maxTurns[0]
	}

	workspaceTools := tools.NewWorkspaceTools()
	return &Agent{
		root:           root,
		tools:          workspaceTools.Definitions(),
		model:          model,
		instructions:   instructions,
		maxTurns:       turns,
		workspaceTools: workspaceTools,
	}, nil
}

func (agent *Agent) SetWriteApprover(approver tools.WriteApprover) {
	if agent.workspaceTools != nil {
		agent.workspaceTools.SetWriteApprover(approver)
	}
}

func (agent *Agent) SetCommandApprover(approver tools.CommandApprover) {
	if agent.workspaceTools != nil {
		agent.workspaceTools.SetCommandApprover(approver)
	}
}

// Run continues the conversation until the model returns a final response.
func (agent *Agent) Run(ctx context.Context, task string, onTextDelta TextDeltaHandler) (result RunResult, runErr error) {
	if task == "" {
		return RunResult{}, fmt.Errorf("agent run: task is empty")
	}
	if agent.model == nil {
		return RunResult{}, fmt.Errorf("agent run: model is nil")
	}
	if agent.maxTurns <= 0 {
		return RunResult{}, fmt.Errorf("agent run: max turns must be positive")
	}
	if shouldCompact(agent.tokenUsage, defaultContextWindow) {
		if err := agent.Compact(ctx); err != nil {
			return RunResult{}, fmt.Errorf("agent run: automatic compact: %w", err)
		}
	}

	definitions := modelTools(agent.tools)
	currentTrace, err := agent.startRunTrace(task, definitions)
	if err != nil {
		reportTraceError("run_start", err)
	}
	defer func() {
		if err := currentTrace.finish(runErr, agent.hasUnverifiedChange, agent.lastVerification); err != nil {
			reportTraceError("run_end", err)
		}
	}()

	messages := agent.messages
	messages = append(messages, Message{Role: "user", Content: task})
	for turn := 0; turn < agent.maxTurns; turn++ {
		request := ModelRequest{
			Instructions: agent.instructions,
			Messages:     messages,
			Tools:        definitions,
		}
		response, err := agent.callModel(ctx, request, onTextDelta, currentTrace, turn+1)
		if err != nil {
			return RunResult{}, fmt.Errorf("agent run: %w", err)
		}

		messages = append(messages, response.Message)
		if len(response.Message.ToolCalls) == 0 {
			tokenUsage := response.Usage.TotalTokens
			if tokenUsage == 0 {
				estimatedTokens, estimateErr := agent.model.EstimateRequestTokens(ModelRequest{
					Instructions: agent.instructions,
					Messages:     messages,
					Tools:        definitions,
				})
				if estimateErr == nil {
					tokenUsage = estimatedTokens
				}
			}
			agent.messages = messages
			agent.tokenUsage = tokenUsage
			return RunResult{Content: response.Message.Content}, nil
		}
		if turn+1 == agent.maxTurns {
			return RunResult{}, fmt.Errorf("agent run: reached maximum of %d turns", agent.maxTurns)
		}

		results := make([]ToolResult, 0, len(response.Message.ToolCalls))
		for _, call := range response.Message.ToolCalls {
			result, err := agent.callTool(ctx, call, currentTrace, turn+1)
			if err != nil {
				return RunResult{}, err
			}
			results = append(results, result)
		}
		messages = append(messages, Message{Role: "tool", ToolResults: results})
	}

	return RunResult{}, fmt.Errorf("agent run: reached maximum of %d turns", agent.maxTurns)
}

func (agent *Agent) callModel(ctx context.Context, request ModelRequest, onTextDelta TextDeltaHandler, current *runTrace, turn int) (ModelResponse, error) {
	startedAt := time.Now()
	if err := current.append(trace.Event{
		Timestamp: startedAt.UTC(),
		Type:      "model_request",
		Turn:      turn,
	}); err != nil {
		reportTraceError("model_request", err)
	}

	stream, err := agent.model.GenerateResponse(ctx, request)
	var response *ModelResponse
	if err == nil {
		defer stream.Close()
		for {
			event, receiveErr := stream.Recv()
			if receiveErr == io.EOF {
				err = fmt.Errorf("generate response: model stream ended without final response")
				break
			}
			if receiveErr != nil {
				err = fmt.Errorf("generate response: %w", receiveErr)
				break
			}
			if event.Response != nil {
				response = event.Response
				break
			}
			if event.TextDelta != "" && onTextDelta != nil {
				if handleErr := onTextDelta(event.TextDelta); handleErr != nil {
					err = fmt.Errorf("write streamed response: %w", handleErr)
					break
				}
			}
		}
	}
	var data map[string]any
	if err != nil {
		data = map[string]any{"error": err.Error()}
	} else {
		data = map[string]any{"content": response.Message.Content, "toolCalls": response.Message.ToolCalls}
	}
	if traceErr := current.append(trace.Event{
		Timestamp:  time.Now().UTC(),
		Type:       "model_response",
		Turn:       turn,
		DurationMS: time.Since(startedAt).Milliseconds(),
		Data:       data,
	}); traceErr != nil {
		reportTraceError("model_response", traceErr)
	}
	if err != nil {
		return ModelResponse{}, err
	}
	return *response, nil
}

func (agent *Agent) callTool(ctx context.Context, call ToolCall, current *runTrace, turn int) (ToolResult, error) {
	startedAt := time.Now()
	if err := current.append(trace.Event{
		Timestamp: startedAt.UTC(),
		Type:      "tool_call",
		Turn:      turn,
		Data:      map[string]any{"id": call.ID, "name": call.Name, "arguments": call.Arguments},
	}); err != nil {
		reportTraceError("tool_call", err)
	}

	result, err := agent.executeToolCall(ctx, call)
	var data map[string]any
	if err != nil {
		data = map[string]any{"id": call.ID, "error": err.Error()}
	} else {
		data = map[string]any{"id": call.ID, "content": result.Content, "isError": result.IsError}
	}
	if traceErr := current.append(trace.Event{
		Timestamp:  time.Now().UTC(),
		Type:       "tool_result",
		Turn:       turn,
		DurationMS: time.Since(startedAt).Milliseconds(),
		Data:       data,
	}); traceErr != nil {
		reportTraceError("tool_result", traceErr)
	}
	if err != nil {
		return ToolResult{}, fmt.Errorf("agent run: %w", err)
	}
	return result, nil
}

// Reset clears the conversation history and starts a new traced session.
func (agent *Agent) Reset() error {
	if agent.traceWriter != nil {
		newSessionID, err := newTraceID("session")
		if err != nil {
			reportTraceError("session_reset", err)
		} else {
			if err := agent.traceWriter.Append(trace.Event{
				Timestamp: time.Now().UTC(),
				SessionID: agent.sessionID,
				Type:      "session_reset",
				Data:      map[string]any{"newSessionId": newSessionID},
			}); err != nil {
				reportTraceError("session_reset", err)
			}
			agent.sessionID = newSessionID
		}
	}
	agent.messages = nil
	agent.tokenUsage = 0
	agent.hasUnverifiedChange = false
	agent.lastVerification = nil
	if agent.workspaceTools != nil {
		agent.workspaceTools.ResetReadState()
	}
	return nil
}
