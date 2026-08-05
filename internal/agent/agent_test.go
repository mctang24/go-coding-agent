package agent

import (
	"context"
	"errors"
	"github.com/goccy/go-json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go-coding-agent/internal/tools"
	"go-coding-agent/internal/trace"
)

type modelStub struct {
	responses       []ModelResponse
	errs            []error
	requests        []ModelRequest
	estimatedTokens int
	estimateErr     error
	estimateCalls   int
	estimateRequest ModelRequest
}

func TestAgentEnableTrace(t *testing.T) {
	agent := Agent{}
	if err := agent.EnableTrace(trace.Writer{Path: filepath.Join(t.TempDir(), "trace.jsonl")}); err != nil {
		t.Fatalf("EnableTrace() error = %v", err)
	}
	if agent.traceWriter == nil || !sessionIDPattern.MatchString(agent.sessionID) {
		t.Fatalf("trace writer = %#v, session ID = %q", agent.traceWriter, agent.sessionID)
	}
	if err := agent.EnableTrace(trace.Writer{}); err == nil {
		t.Fatal("EnableTrace() error = nil, want empty path error")
	}
}

func TestNewAgent(t *testing.T) {
	model := &modelStub{}
	created, err := NewAgent(t.TempDir(), model, "inspect")
	if err != nil {
		t.Fatalf("NewAgent() error = %v", err)
	}
	if created.model != model || created.instructions != "inspect" || created.maxTurns != defaultMaxTurns {
		t.Fatalf("NewAgent() = %#v", created)
	}
	if len(created.tools) != 7 {
		t.Fatalf("NewAgent() tool count = %d, want 7", len(created.tools))
	}

	configured, err := NewAgent(t.TempDir(), model, "", 3)
	if err != nil {
		t.Fatalf("NewAgent() with max turns error = %v", err)
	}
	if configured.maxTurns != 3 {
		t.Fatalf("NewAgent() max turns = %d, want 3", configured.maxTurns)
	}
}

func TestNewAgentErrors(t *testing.T) {
	model := &modelStub{}
	tests := []struct {
		name     string
		root     string
		model    Model
		maxTurns []int
		wantErr  string
	}{
		{name: "empty root", model: model, wantErr: "root is empty"},
		{name: "nil model", root: t.TempDir(), wantErr: "model is nil"},
		{name: "invalid max turns", root: t.TempDir(), model: model, maxTurns: []int{0}, wantErr: "max turns must be positive"},
		{name: "multiple max turns", root: t.TempDir(), model: model, maxTurns: []int{1, 2}, wantErr: "at most one"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewAgent(tt.root, tt.model, "", tt.maxTurns...)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("NewAgent() error = %v, want to contain %q", err, tt.wantErr)
			}
		})
	}
}

func (stub *modelStub) GenerateResponse(_ context.Context, request ModelRequest) (ModelStream, error) {
	stub.requests = append(stub.requests, request)
	if len(stub.errs) > 0 {
		err := stub.errs[0]
		stub.errs = stub.errs[1:]
		if err != nil {
			return nil, err
		}
	}
	response := stub.responses[0]
	stub.responses = stub.responses[1:]
	return &stubModelStream{response: response}, nil
}

func (stub *modelStub) EstimateRequestTokens(request ModelRequest) (int, error) {
	stub.estimateCalls++
	stub.estimateRequest = request
	return stub.estimatedTokens, stub.estimateErr
}

type stubModelStream struct {
	response ModelResponse
	sentText bool
	finished bool
	err      error
}

func (stream *stubModelStream) Recv() (ModelStreamEvent, error) {
	if stream.finished {
		return ModelStreamEvent{}, io.EOF
	}
	if stream.sentText {
		if stream.err != nil {
			return ModelStreamEvent{}, stream.err
		}
		stream.finished = true
		return ModelStreamEvent{Response: &stream.response}, nil
	}
	stream.sentText = true
	if stream.response.Message.Content == "" {
		stream.finished = true
		return ModelStreamEvent{Response: &stream.response}, nil
	}
	return ModelStreamEvent{TextDelta: stream.response.Message.Content}, nil
}

func (stream *stubModelStream) Close() error { return nil }

func finishTaskCall(id, result string) ToolCall {
	arguments, err := json.Marshal(tools.FinishTaskInput{Result: result})
	if err != nil {
		panic(err)
	}
	return ToolCall{ID: id, Name: "finish_task", Arguments: string(arguments)}
}

func verifiedFinishTaskCall(id, result string) ToolCall {
	arguments, err := json.Marshal(tools.FinishTaskInput{Result: result, Command: "go", Args: []string{"test", "./..."}})
	if err != nil {
		panic(err)
	}
	return ToolCall{ID: id, Name: "finish_task", Arguments: string(arguments)}
}

func finishTaskTool(result string, verification *tools.CommandResult) tools.Tool {
	output, err := json.Marshal(tools.FinishTaskResult{Result: result, Verification: verification})
	if err != nil {
		panic(err)
	}
	return tools.Tool{Name: "finish_task", Execute: func(context.Context, string, string) (string, error) {
		return string(output), nil
	}}
}

func TestAgentRun(t *testing.T) {
	model := &modelStub{responses: []ModelResponse{
		{Message: Message{Role: "assistant", Content: "checking", ToolCalls: []ToolCall{{ID: "call_1", Name: "echo", Arguments: `{}`}}}},
		{Message: Message{Role: "assistant", ToolCalls: []ToolCall{finishTaskCall("finish", "done")}}},
	}}
	agent := Agent{
		model:        model,
		maxTurns:     2,
		instructions: "inspect",
		tools: []tools.Tool{
			{Name: "echo", Execute: func(context.Context, string, string) (string, error) {
				return "result", nil
			}},
			finishTaskTool("done", nil),
		},
	}

	var streamed strings.Builder
	result, err := agent.Run(context.Background(), "task", func(delta string) error {
		streamed.WriteString(delta)
		return nil
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Content != "done" || result.Status != RunStatusSuccess {
		t.Fatalf("Run() = %#v", result)
	}
	if streamed.String() != "checkingdone" {
		t.Fatalf("streamed output = %q, want checkingdone", streamed.String())
	}
	if len(model.requests) != 2 || len(model.requests[1].Messages) != 3 {
		t.Fatalf("model requests = %#v", model.requests)
	}
	if model.requests[0].Instructions != "inspect" || model.requests[1].Instructions != "inspect" {
		t.Fatalf("model instructions = %q, %q", model.requests[0].Instructions, model.requests[1].Instructions)
	}
	toolMessage := model.requests[1].Messages[2]
	if toolMessage.Role != "tool" || len(toolMessage.ToolResults) != 1 || toolMessage.ToolResults[0].Content != "result" {
		t.Fatalf("tool message = %#v", toolMessage)
	}
}

func TestAgentRunStopsWithoutFinishIncomplete(t *testing.T) {
	runner := Agent{
		model:    &modelStub{responses: []ModelResponse{{Message: Message{Role: "assistant", Content: "not finished"}}}},
		maxTurns: 1,
	}

	result, err := runner.Run(context.Background(), "task", nil)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Content != "not finished" || result.Status != RunStatusIncomplete {
		t.Fatalf("Run() = %#v", result)
	}
}

func TestAgentRunRejectsMixedFinishTask(t *testing.T) {
	executed := false
	model := &modelStub{responses: []ModelResponse{
		{Message: Message{Role: "assistant", ToolCalls: []ToolCall{
			{ID: "edit", Name: "edit_file", Arguments: `{}`},
			finishTaskCall("mixed_finish", "too early"),
		}}},
		{Message: Message{Role: "assistant", ToolCalls: []ToolCall{finishTaskCall("finish", "done")}}},
	}}
	runner := Agent{
		model:    model,
		maxTurns: 2,
		tools: []tools.Tool{
			{Name: "edit_file", Execute: func(context.Context, string, string) (string, error) {
				executed = true
				return "edited", nil
			}},
			finishTaskTool("done", nil),
		},
	}

	result, err := runner.Run(context.Background(), "task", nil)
	if err != nil || result.Status != RunStatusSuccess {
		t.Fatalf("Run() = %#v, error = %v", result, err)
	}
	if executed {
		t.Fatal("mixed edit_file was executed")
	}
	results := model.requests[1].Messages[2].ToolResults
	if len(results) != 2 || !results[0].IsError || !results[1].IsError || !strings.Contains(results[0].Content, "only tool call") {
		t.Fatalf("mixed tool results = %#v", results)
	}
}

func TestAgentRunKeepsFinishToolResultInHistory(t *testing.T) {
	model := &modelStub{responses: []ModelResponse{
		{Message: Message{Role: "assistant", ToolCalls: []ToolCall{finishTaskCall("finish_1", "first")}}},
		{Message: Message{Role: "assistant", ToolCalls: []ToolCall{finishTaskCall("finish_2", "second")}}},
	}}
	runner, err := NewAgent(t.TempDir(), model, "")
	if err != nil {
		t.Fatal(err)
	}

	first, err := runner.Run(context.Background(), "first task", nil)
	if err != nil || first.Status != RunStatusSuccess || first.Content != "first" {
		t.Fatalf("first Run() = %#v, error = %v", first, err)
	}
	second, err := runner.Run(context.Background(), "second task", nil)
	if err != nil || second.Status != RunStatusSuccess || second.Content != "second" {
		t.Fatalf("second Run() = %#v, error = %v", second, err)
	}

	messages := model.requests[1].Messages
	if len(messages) != 4 || messages[1].ToolCalls[0].ID != "finish_1" || messages[2].ToolResults[0].ToolCallID != "finish_1" || messages[3].Content != "second task" {
		t.Fatalf("second request messages = %#v", messages)
	}
}

func TestAgentRunFinishUsesReportedTokenUsage(t *testing.T) {
	model := &modelStub{responses: []ModelResponse{{
		Message: Message{Role: "assistant", ToolCalls: []ToolCall{finishTaskCall("finish", "done")}},
		Usage:   TokenUsage{PromptTokens: 12, CompletionTokens: 3, TotalTokens: 15},
	}}}
	runner, err := NewAgent(t.TempDir(), model, "")
	if err != nil {
		t.Fatal(err)
	}

	if _, err := runner.Run(context.Background(), "task", nil); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if runner.tokenUsage != 15 {
		t.Fatalf("token usage = %d, want 15", runner.tokenUsage)
	}
	if model.estimateCalls != 0 {
		t.Fatalf("estimate calls = %d, want 0", model.estimateCalls)
	}
}

func TestAgentRunReturnsToolExecutionErrorToModel(t *testing.T) {
	model := &modelStub{responses: []ModelResponse{
		{Message: Message{Role: "assistant", ToolCalls: []ToolCall{{ID: "call_1", Name: "failing", Arguments: `{}`}}}},
		{Message: Message{Role: "assistant", Content: "recovered"}},
	}}
	agent := Agent{
		model:    model,
		maxTurns: 2,
		tools: []tools.Tool{{Name: "failing", Execute: func(context.Context, string, string) (string, error) {
			return "", errors.New("tool failed")
		}}},
	}

	result, err := agent.Run(context.Background(), "task", nil)
	if err != nil || result.Content != "recovered" {
		t.Fatalf("Run() = %#v, error = %v", result, err)
	}
	toolResults := model.requests[1].Messages[2].ToolResults
	if len(toolResults) != 1 || !toolResults[0].IsError || !strings.Contains(toolResults[0].Content, "tool failed") {
		t.Fatalf("tool results = %#v", toolResults)
	}
}

func TestAgentRunAutomaticallyCompactsAtThreshold(t *testing.T) {
	model := &modelStub{
		responses: []ModelResponse{
			{Message: Message{Role: "assistant", Content: validSummary()}},
			{Message: Message{Role: "assistant", ToolCalls: []ToolCall{finishTaskCall("finish", "done")}}},
		},
	}
	runner := Agent{
		model:    model,
		maxTurns: 1,
		tools:    []tools.Tool{finishTaskTool("done", nil)},
		messages: append(compactableHistory(),
			Message{Role: "user", Content: "fourth question"},
			Message{Role: "assistant", Content: "fourth answer"},
		),
		tokenUsage: defaultContextWindow*compactThresholdPercent/100 + 1,
	}

	result, err := runner.Run(context.Background(), "new question", nil)
	if err != nil || result.Content != "done" {
		t.Fatalf("Run() = %#v, error = %v, requests = %#v", result, err, model.requests)
	}
	if len(model.requests) != 2 {
		t.Fatalf("model request count = %d, want 2", len(model.requests))
	}
	if model.requests[0].Instructions != summaryInstructions {
		t.Fatalf("first request instructions = %q, want compact instructions", model.requests[0].Instructions)
	}
	if !isSummaryMessage(model.requests[1].Messages[0]) {
		t.Fatalf("automatic compact summary missing from request: %#v", model.requests[1].Messages)
	}
}

func TestAgentRunEditsFile(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "file.txt")
	if err := os.WriteFile(path, []byte("before"), 0o600); err != nil {
		t.Fatal(err)
	}
	model := &modelStub{responses: []ModelResponse{
		{Message: Message{Role: "assistant", ToolCalls: []ToolCall{{ID: "read", Name: "read_file", Arguments: `{"path":"file.txt"}`}}}},
		{Message: Message{Role: "assistant", ToolCalls: []ToolCall{{ID: "edit", Name: "edit_file", Arguments: `{"path":"file.txt","old_string":"before","new_string":"after"}`}}}},
		{Message: Message{Role: "assistant", Content: "done"}},
	}}
	runner, err := NewAgent(root, model, "")
	if err != nil {
		t.Fatal(err)
	}
	runner.SetWriteApprover(func(context.Context, tools.WriteRequest) (bool, error) { return true, nil })
	result, err := runner.Run(context.Background(), "edit file", nil)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	content, err := os.ReadFile(path)
	if err != nil || string(content) != "after" || result.Content != "done" {
		t.Fatalf("content = %q, result = %#v, error = %v", content, result, err)
	}
	if len(model.requests) != 3 || len(model.requests[2].Messages) != 5 {
		t.Fatalf("requests = %#v", model.requests)
	}
	toolResults := model.requests[2].Messages[4].ToolResults
	if len(toolResults) != 1 || toolResults[0].IsError || !strings.Contains(toolResults[0].Content, "replaced 1 occurrence") {
		t.Fatalf("tool results = %#v", toolResults)
	}
}

func TestAgentRunReturnsWriteDenialToModel(t *testing.T) {
	root := t.TempDir()
	model := &modelStub{responses: []ModelResponse{
		{Message: Message{Role: "assistant", ToolCalls: []ToolCall{{ID: "write", Name: "write_file", Arguments: `{"path":"new.txt","content":"data"}`}}}},
		{Message: Message{Role: "assistant", Content: "cancelled"}},
	}}
	runner, err := NewAgent(root, model, "")
	if err != nil {
		t.Fatal(err)
	}
	runner.SetWriteApprover(func(context.Context, tools.WriteRequest) (bool, error) { return false, nil })
	result, err := runner.Run(context.Background(), "create file", nil)
	if err != nil || result.Content != "cancelled" {
		t.Fatalf("result = %#v, error = %v", result, err)
	}
	if result.Status != RunStatusIncomplete {
		t.Fatalf("status = %q, want incomplete", result.Status)
	}
	if _, err := os.Stat(filepath.Join(root, "new.txt")); !os.IsNotExist(err) {
		t.Fatalf("new file stat error = %v", err)
	}
	toolResults := model.requests[1].Messages[2].ToolResults
	if len(toolResults) != 1 || !toolResults[0].IsError || !strings.Contains(toolResults[0].Content, "user denied") {
		t.Fatalf("tool results = %#v", toolResults)
	}
	if runner.hasUnverifiedChange || runner.lastVerification != nil {
		t.Fatalf("denied write changed verification state: %#v", runner)
	}
}

func TestAgentRunEndVerificationStatus(t *testing.T) {
	tests := []struct {
		name              string
		initialUnverified bool
		calls             []ToolCall
		tools             []tools.Tool
		wantStatus        RunStatus
		wantVerification  bool
		wantExitCode      float64
	}{
		{
			name:  "successful edit is incomplete",
			calls: []ToolCall{{ID: "edit", Name: "edit_file", Arguments: `{}`}},
			tools: []tools.Tool{{Name: "edit_file", Execute: func(context.Context, string, string) (string, error) {
				return "edited", nil
			}}},
			wantStatus: RunStatusIncomplete,
		},
		{
			name:              "finish with successful verification completes changes",
			initialUnverified: true,
			calls:             []ToolCall{verifiedFinishTaskCall("finish", "done")},
			tools:             []tools.Tool{finishTaskTool("done", &tools.CommandResult{Stdout: "ok"})},
			wantStatus:        RunStatusSuccess,
			wantVerification:  true,
			wantExitCode:      0,
		},
		{
			name:              "failed finish verification remains incomplete",
			initialUnverified: true,
			calls:             []ToolCall{verifiedFinishTaskCall("finish", "done")},
			tools:             []tools.Tool{finishTaskTool("done", &tools.CommandResult{ExitCode: 1, Stderr: "failed"})},
			wantStatus:        RunStatusIncomplete,
			wantVerification:  true,
			wantExitCode:      1,
		},
		{
			name:              "finish without required verification remains incomplete",
			initialUnverified: true,
			calls:             []ToolCall{finishTaskCall("finish", "done")},
			tools:             []tools.Tool{finishTaskTool("done", nil)},
			wantStatus:        RunStatusIncomplete,
		},
		{
			name:       "finish without changes succeeds",
			calls:      []ToolCall{finishTaskCall("finish", "done")},
			tools:      []tools.Tool{finishTaskTool("done", nil)},
			wantStatus: RunStatusSuccess,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "trace.jsonl")
			responses := make([]ModelResponse, 0, len(test.calls)+1)
			for _, call := range test.calls {
				responses = append(responses, ModelResponse{Message: Message{Role: "assistant", ToolCalls: []ToolCall{call}}})
			}
			if test.wantStatus == RunStatusIncomplete {
				responses = append(responses, ModelResponse{Message: Message{Role: "assistant", Content: "done"}})
			}
			runner := Agent{
				model:               &modelStub{responses: responses},
				maxTurns:            len(responses),
				tools:               test.tools,
				hasUnverifiedChange: test.initialUnverified,
			}
			if err := runner.EnableTrace(trace.Writer{Path: path}); err != nil {
				t.Fatal(err)
			}

			result, err := runner.Run(context.Background(), "task", nil)
			if err != nil {
				t.Fatalf("Run() error = %v", err)
			}
			if result.Status != test.wantStatus {
				t.Fatalf("Run() status = %q, want %q", result.Status, test.wantStatus)
			}

			events := readTraceEvents(t, path)
			runEnd := events[len(events)-1].Data.(map[string]any)
			if runEnd["status"] != string(test.wantStatus) {
				t.Fatalf("run_end = %#v, want status %q", runEnd, test.wantStatus)
			}
			verification, exists := runEnd["verification"]
			if exists != test.wantVerification {
				t.Fatalf("run_end verification = %#v, want present %t", verification, test.wantVerification)
			}
			if test.wantVerification {
				fact := verification.(map[string]any)
				if fact["tool"] != "finish_task" || fact["command"] != "go test ./..." || fact["exitCode"] != test.wantExitCode {
					t.Fatalf("verification = %#v", fact)
				}
			}
		})
	}
}

func TestAgentRunTrace(t *testing.T) {
	path := filepath.Join(t.TempDir(), "trace.jsonl")
	model := &modelStub{responses: []ModelResponse{
		{Message: Message{Role: "assistant", ToolCalls: []ToolCall{{ID: "call_1", Name: "fail", Arguments: `{}`}}}},
		{Message: Message{Role: "assistant", ToolCalls: []ToolCall{finishTaskCall("finish", "done")}}},
	}}
	agent := Agent{
		model:        model,
		maxTurns:     2,
		instructions: "inspect carefully",
		tools: []tools.Tool{
			{Name: "fail", Description: "always fails", Parameters: tools.ObjectSchema(nil), Execute: func(context.Context, string, string) (string, error) {
				return "", errors.New("tool failed")
			}},
			finishTaskTool("done", nil),
		},
	}
	if err := agent.EnableTrace(trace.Writer{Path: path}); err != nil {
		t.Fatalf("EnableTrace() error = %v", err)
	}

	if _, err := agent.Run(context.Background(), "task", nil); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	events := readTraceEvents(t, path)
	wantTypes := []string{"run_start", "model_request", "model_response", "tool_call", "tool_result", "model_request", "model_response", "tool_call", "tool_result", "run_end"}
	if len(events) != len(wantTypes) {
		t.Fatalf("trace event count = %d, want %d", len(events), len(wantTypes))
	}
	runID := events[0].RunID
	for index, event := range events {
		if event.Type != wantTypes[index] || event.SessionID != agent.sessionID || event.RunID != runID || event.Timestamp.IsZero() {
			t.Fatalf("trace event %d = %#v", index, event)
		}
	}
	if !strings.HasPrefix(runID, "run_") || events[1].Turn != 1 || events[5].Turn != 2 {
		t.Fatalf("run ID = %q, turns = %d, %d", runID, events[1].Turn, events[5].Turn)
	}
	if events[1].Data != nil || events[5].Data != nil {
		t.Fatalf("model request data = %#v, %#v", events[1].Data, events[5].Data)
	}
	runStart := events[0].Data.(map[string]any)
	traceTools := runStart["tools"].([]any)
	traceTool := traceTools[0].(map[string]any)
	if len(runStart) != 3 || runStart["task"] != "task" || runStart["instructions"] != "inspect carefully" || len(traceTools) != 2 || traceTool["name"] != "fail" || traceTool["description"] != "always fails" {
		t.Fatalf("run start data = %#v", runStart)
	}
	toolResult := events[4].Data.(map[string]any)
	if toolResult["isError"] != true || !strings.Contains(toolResult["content"].(string), "tool failed") {
		t.Fatalf("tool result data = %#v", toolResult)
	}
	runEnd := events[9].Data.(map[string]any)
	if runEnd["status"] != "success" {
		t.Fatalf("run end data = %#v", runEnd)
	}
}

func TestAgentRunTraceError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "trace.jsonl")
	agent := Agent{model: errorModel{err: errors.New("model failed")}, maxTurns: 1}
	if err := agent.EnableTrace(trace.Writer{Path: path}); err != nil {
		t.Fatalf("EnableTrace() error = %v", err)
	}

	result, err := agent.Run(context.Background(), "task", nil)
	if err == nil {
		t.Fatal("Run() error = nil")
	}
	if result.Status != RunStatusError {
		t.Fatalf("Run() status = %q, want error", result.Status)
	}

	events := readTraceEvents(t, path)
	wantTypes := []string{"run_start", "model_request", "model_response", "run_end"}
	if len(events) != len(wantTypes) {
		t.Fatalf("trace event count = %d, want %d", len(events), len(wantTypes))
	}
	for index, event := range events {
		if event.Type != wantTypes[index] {
			t.Fatalf("trace event %d type = %q, want %q", index, event.Type, wantTypes[index])
		}
	}
	response := events[2].Data.(map[string]any)
	runEnd := events[3].Data.(map[string]any)
	if !strings.Contains(response["error"].(string), "model failed") || runEnd["status"] != "error" || !strings.Contains(runEnd["error"].(string), "model failed") {
		t.Fatalf("model response = %#v, run end = %#v", response, runEnd)
	}
}

func TestAgentRunIgnoresTraceStartError(t *testing.T) {
	model := &modelStub{responses: []ModelResponse{{Message: Message{Role: "assistant", Content: "done"}}}}
	agent := Agent{model: model, maxTurns: 1}
	badPath := filepath.Join(t.TempDir(), "missing", "trace.jsonl")
	if err := agent.EnableTrace(trace.Writer{Path: badPath}); err != nil {
		t.Fatalf("EnableTrace() error = %v", err)
	}

	result, err := agent.Run(context.Background(), "task", nil)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Content != "done" || len(agent.messages) != 2 {
		t.Fatalf("Run() = %#v, messages = %#v", result, agent.messages)
	}
}

func readTraceEvents(t *testing.T, path string) []trace.Event {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read trace: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	events := make([]trace.Event, 0, len(lines))
	for _, line := range lines {
		var event trace.Event
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatalf("decode trace: %v", err)
		}
		events = append(events, event)
	}
	return events
}

func TestAgentRunContinuesConversation(t *testing.T) {
	model := &modelStub{responses: []ModelResponse{
		{Message: Message{Role: "assistant", Content: "first answer"}},
		{Message: Message{Role: "assistant", Content: "second answer"}},
	}}
	agent := Agent{model: model, maxTurns: 1}

	if _, err := agent.Run(context.Background(), "first question", nil); err != nil {
		t.Fatalf("first Run() error = %v", err)
	}
	if _, err := agent.Run(context.Background(), "second question", nil); err != nil {
		t.Fatalf("second Run() error = %v", err)
	}

	messages := model.requests[1].Messages
	if len(messages) != 3 || messages[0].Content != "first question" || messages[1].Content != "first answer" || messages[2].Content != "second question" {
		t.Fatalf("second request messages = %#v", messages)
	}
}

func TestAgentRunStoresTokenUsage(t *testing.T) {
	model := &modelStub{responses: []ModelResponse{{
		Message: Message{Role: "assistant", Content: "done"},
		Usage:   TokenUsage{PromptTokens: 12, CompletionTokens: 3, TotalTokens: 15},
	}}}
	agent := Agent{model: model, maxTurns: 1}

	if _, err := agent.Run(context.Background(), "question", nil); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if agent.tokenUsage != 15 {
		t.Fatalf("token usage = %d, want 15", agent.tokenUsage)
	}
	if model.estimateCalls != 0 {
		t.Fatalf("estimate calls = %d, want 0", model.estimateCalls)
	}
}

func TestAgentRunClearsMissingTokenUsage(t *testing.T) {
	model := &modelStub{
		responses:   []ModelResponse{{Message: Message{Role: "assistant", Content: "done"}}},
		estimateErr: errors.New("estimate failed"),
	}
	agent := Agent{model: model, maxTurns: 1, tokenUsage: 99}

	if _, err := agent.Run(context.Background(), "question", nil); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if agent.tokenUsage != 0 {
		t.Fatalf("token usage = %d, want unknown usage 0", agent.tokenUsage)
	}
}

func TestAgentRunUsesEstimatedTokenUsageWhenMissing(t *testing.T) {
	model := &modelStub{
		responses:       []ModelResponse{{Message: Message{Role: "assistant", Content: "done"}}},
		estimatedTokens: 23,
	}
	agent := Agent{model: model, maxTurns: 1}

	if _, err := agent.Run(context.Background(), "question", nil); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if agent.tokenUsage != 23 {
		t.Fatalf("token usage = %d, want estimate 23", agent.tokenUsage)
	}
	if model.estimateCalls != 1 {
		t.Fatalf("estimate calls = %d, want 1", model.estimateCalls)
	}
	messages := model.estimateRequest.Messages
	if len(messages) != 2 || messages[1].Role != "assistant" || messages[1].Content != "done" {
		t.Fatalf("estimated request messages = %#v, want committed user and assistant messages", messages)
	}
}

func TestAgentRunDiscardsFailedConversation(t *testing.T) {
	model := &modelStub{responses: []ModelResponse{
		{Message: Message{Role: "assistant", ToolCalls: []ToolCall{{ID: "call_1"}}}},
		{Message: Message{Role: "assistant", Content: "done"}},
	}}
	agent := Agent{model: model, maxTurns: 1}

	if _, err := agent.Run(context.Background(), "failed question", nil); err == nil {
		t.Fatal("first Run() error = nil")
	}
	if _, err := agent.Run(context.Background(), "new question", nil); err != nil {
		t.Fatalf("second Run() error = %v", err)
	}

	messages := model.requests[1].Messages
	if len(messages) != 1 || messages[0].Content != "new question" {
		t.Fatalf("second request messages = %#v", messages)
	}
}

type interruptedModel struct {
	cancel   context.CancelCauseFunc
	requests []ModelRequest
}

func (model *interruptedModel) GenerateResponse(ctx context.Context, request ModelRequest) (ModelStream, error) {
	model.requests = append(model.requests, request)
	return &interruptedStream{ctx: ctx, cancel: model.cancel}, nil
}

func (*interruptedModel) EstimateRequestTokens(ModelRequest) (int, error) {
	return 0, nil
}

type interruptedStream struct {
	ctx    context.Context
	cancel context.CancelCauseFunc
	sent   bool
}

func (stream *interruptedStream) Recv() (ModelStreamEvent, error) {
	if !stream.sent {
		stream.sent = true
		stream.cancel(ErrRunInterrupted)
		return ModelStreamEvent{TextDelta: "partial"}, nil
	}
	return ModelStreamEvent{}, stream.ctx.Err()
}

func (*interruptedStream) Close() error { return nil }

func TestAgentRunPersistsModelInterruptionWithoutPartialResponse(t *testing.T) {
	ctx, cancel := context.WithCancelCause(context.Background())
	model := &interruptedModel{cancel: cancel}
	runner, err := newSessionAgent(t.TempDir(), t.TempDir(), model, "", "")
	if err != nil {
		t.Fatalf("newSessionAgent() error = %v", err)
	}
	var output strings.Builder

	result, err := runner.Run(ctx, "question", func(delta string) error {
		output.WriteString(delta)
		return nil
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Status != RunStatusInterrupted || output.String() != "partial" {
		t.Fatalf("Run() = %#v, output = %q", result, output.String())
	}
	if len(runner.messages) != 2 || runner.messages[0].Content != "question" || runner.messages[1].Content != interruptedMessage {
		t.Fatalf("messages = %#v", runner.messages)
	}

	restored, err := newSessionAgent(filepath.Dir(runner.sessionFile.path), runner.root, &modelStub{}, "", runner.SessionID())
	if err != nil {
		t.Fatalf("restore newSessionAgent() error = %v", err)
	}
	if len(restored.messages) != 2 || restored.messages[1].Content != interruptedMessage {
		t.Fatalf("restored messages = %#v", restored.messages)
	}
}

func TestAgentRunCompletesInterruptedToolRound(t *testing.T) {
	ctx, cancel := context.WithCancelCause(context.Background())
	model := &modelStub{responses: []ModelResponse{{
		Message: Message{Role: "assistant", ToolCalls: []ToolCall{
			{ID: "call_1", Name: "first", Arguments: `{}`},
			{ID: "call_2", Name: "second", Arguments: `{}`},
			{ID: "call_3", Name: "third", Arguments: `{}`},
		}},
		Usage: TokenUsage{TotalTokens: 37},
	}}}
	thirdCalls := 0
	runner, err := newSessionAgent(t.TempDir(), t.TempDir(), model, "", "")
	if err != nil {
		t.Fatalf("newSessionAgent() error = %v", err)
	}
	runner.tools = []tools.Tool{
		{Name: "first", Execute: func(context.Context, string, string) (string, error) {
			return "completed", nil
		}},
		{Name: "second", Execute: func(context.Context, string, string) (string, error) {
			cancel(ErrRunInterrupted)
			return "", ctx.Err()
		}},
		{Name: "third", Execute: func(context.Context, string, string) (string, error) {
			thirdCalls++
			return "unexpected", nil
		}},
	}

	result, err := runner.Run(ctx, "question", nil)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Status != RunStatusInterrupted || thirdCalls != 0 {
		t.Fatalf("Run() = %#v, third calls = %d", result, thirdCalls)
	}
	if len(runner.messages) != 4 {
		t.Fatalf("messages = %#v", runner.messages)
	}
	results := runner.messages[2].ToolResults
	if len(results) != 3 || results[0].Content != "completed" || results[0].IsError || results[1].Content != "aborted by user" || !results[1].IsError || results[2].Content != "aborted by user" || !results[2].IsError {
		t.Fatalf("tool results = %#v", results)
	}
	if runner.messages[3].Content != interruptedMessage || runner.tokenUsage != 37 {
		t.Fatalf("interruption message = %#v, token usage = %d", runner.messages[3], runner.tokenUsage)
	}

	restored, err := newSessionAgent(filepath.Dir(runner.sessionFile.path), runner.root, &modelStub{}, "", runner.SessionID())
	if err != nil {
		t.Fatalf("restore newSessionAgent() error = %v", err)
	}
	if len(restored.messages) != 4 || len(restored.messages[2].ToolResults) != 3 || restored.messages[3].Content != interruptedMessage {
		t.Fatalf("restored messages = %#v", restored.messages)
	}
}

func TestAgentRunKeepsCompletedToolRoundWhenNextModelRequestFails(t *testing.T) {
	modelErr := errors.New("second request failed")
	model := &modelStub{
		responses: []ModelResponse{{
			Message: Message{Role: "assistant", ToolCalls: []ToolCall{{ID: "call_1", Name: "echo", Arguments: `{}`}}},
			Usage:   TokenUsage{TotalTokens: 29},
		}},
		errs: []error{nil, modelErr},
	}
	agent := Agent{
		model:      model,
		maxTurns:   2,
		messages:   []Message{{Role: "user", Content: "keep"}},
		tokenUsage: 17,
		tools: []tools.Tool{{Name: "echo", Execute: func(context.Context, string, string) (string, error) {
			return "done", nil
		}}},
	}

	_, err := agent.Run(context.Background(), "new question", nil)
	if !errors.Is(err, modelErr) {
		t.Fatalf("Run() error = %v, want model error", err)
	}
	if len(agent.messages) != 4 || agent.messages[0].Content != "keep" || agent.messages[1].Content != "new question" || agent.messages[3].ToolResults[0].Content != "done" {
		t.Fatalf("committed messages = %#v", agent.messages)
	}
	if agent.tokenUsage != 29 {
		t.Fatalf("token usage = %d, want 29", agent.tokenUsage)
	}
}

func TestAgentRunDoesNotSavePartialStream(t *testing.T) {
	modelErr := errors.New("stream failed")
	agent := Agent{
		model:      streamingErrorModel{err: modelErr},
		maxTurns:   1,
		messages:   []Message{{Role: "user", Content: "keep"}},
		tokenUsage: 17,
	}
	var output strings.Builder

	_, err := agent.Run(context.Background(), "new question", func(delta string) error {
		output.WriteString(delta)
		return nil
	})
	if !errors.Is(err, modelErr) {
		t.Fatalf("Run() error = %v, want stream error", err)
	}
	if output.String() != "partial" {
		t.Fatalf("streamed output = %q, want partial", output.String())
	}
	if len(agent.messages) != 1 || agent.messages[0].Content != "keep" {
		t.Fatalf("messages = %#v, want original history", agent.messages)
	}
	if agent.tokenUsage != 17 {
		t.Fatalf("token usage = %d, want original usage 17", agent.tokenUsage)
	}
}

func TestAgentReset(t *testing.T) {
	model := &modelStub{responses: []ModelResponse{
		{Message: Message{Role: "assistant", Content: "first answer"}},
		{Message: Message{Role: "assistant", Content: "second answer"}},
	}}
	agent := Agent{model: model, maxTurns: 1}

	if _, err := agent.Run(context.Background(), "first question", nil); err != nil {
		t.Fatalf("first Run() error = %v", err)
	}
	if err := agent.Reset(); err != nil {
		t.Fatalf("Reset() error = %v", err)
	}
	if _, err := agent.Run(context.Background(), "second question", nil); err != nil {
		t.Fatalf("second Run() error = %v", err)
	}

	messages := model.requests[1].Messages
	if len(messages) != 1 || messages[0].Content != "second question" {
		t.Fatalf("second request messages = %#v", messages)
	}
}

func TestAgentResetTrace(t *testing.T) {
	path := filepath.Join(t.TempDir(), "trace.jsonl")
	agent := Agent{messages: []Message{{Role: "user", Content: "old"}}}
	if err := agent.EnableTrace(trace.Writer{Path: path}); err != nil {
		t.Fatalf("EnableTrace() error = %v", err)
	}
	oldSessionID := agent.sessionID
	if err := agent.Reset(); err != nil {
		t.Fatalf("Reset() error = %v", err)
	}
	if len(agent.messages) != 0 || agent.sessionID == oldSessionID || !sessionIDPattern.MatchString(agent.sessionID) {
		t.Fatalf("messages = %#v, session ID = %q", agent.messages, agent.sessionID)
	}
	if agent.tokenUsage != 0 {
		t.Fatalf("token usage = %d, want 0", agent.tokenUsage)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read trace: %v", err)
	}
	var event trace.Event
	if err := json.Unmarshal(data, &event); err != nil {
		t.Fatalf("decode trace: %v", err)
	}
	if event.Type != "session_reset" || event.SessionID != oldSessionID {
		t.Fatalf("trace event = %#v", event)
	}

	failed := Agent{messages: []Message{{Role: "user", Content: "keep"}}}
	badPath := filepath.Join(t.TempDir(), "missing", "trace.jsonl")
	if err := failed.EnableTrace(trace.Writer{Path: badPath}); err != nil {
		t.Fatalf("EnableTrace() error = %v", err)
	}
	failedSessionID := failed.sessionID
	if err := failed.Reset(); err != nil {
		t.Fatalf("Reset() error = %v", err)
	}
	if len(failed.messages) != 0 || failed.sessionID == failedSessionID {
		t.Fatalf("failed reset messages = %#v, session ID = %q", failed.messages, failed.sessionID)
	}
}

func TestAgentRunErrors(t *testing.T) {
	tests := []struct {
		name    string
		agent   Agent
		ctx     context.Context
		task    string
		wantErr string
	}{
		{name: "empty task", agent: Agent{}, ctx: context.Background(), wantErr: "task is empty"},
		{name: "nil model", agent: Agent{maxTurns: 1}, ctx: context.Background(), task: "task", wantErr: "model is nil"},
		{name: "invalid max turns", agent: Agent{model: &modelStub{}}, ctx: context.Background(), task: "task", wantErr: "max turns must be positive"},
		{name: "maximum turns", agent: Agent{model: &modelStub{responses: []ModelResponse{{Message: Message{ToolCalls: []ToolCall{{ID: "call_1"}}}}}}, maxTurns: 1}, ctx: context.Background(), task: "task", wantErr: "reached maximum"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := tt.agent.Run(tt.ctx, tt.task, nil)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("Run() error = %v, want to contain %q", err, tt.wantErr)
			}
			if result.Status != RunStatusError {
				t.Fatalf("Run() status = %q, want error", result.Status)
			}
		})
	}
}

func TestAgentRunModelError(t *testing.T) {
	modelError := errors.New("failed")
	model := errorModel{err: modelError}
	agent := Agent{model: model, maxTurns: 1}
	_, err := agent.Run(context.Background(), "task", nil)
	if err == nil || err.Error() != "agent run: failed" {
		t.Fatalf("Run() error = %v", err)
	}
	if !errors.Is(err, modelError) {
		t.Fatalf("errors.Is(Run() error, model error) = false")
	}
}

type errorModel struct {
	err error
}

func (model errorModel) GenerateResponse(context.Context, ModelRequest) (ModelStream, error) {
	return nil, model.err
}

func (model errorModel) EstimateRequestTokens(ModelRequest) (int, error) {
	return 0, model.err
}

type streamingErrorModel struct {
	err error
}

func (model streamingErrorModel) GenerateResponse(context.Context, ModelRequest) (ModelStream, error) {
	return &stubModelStream{
		response: ModelResponse{Message: Message{Role: "assistant", Content: "partial"}},
		err:      model.err,
	}, nil
}

func (model streamingErrorModel) EstimateRequestTokens(ModelRequest) (int, error) {
	return 0, model.err
}
