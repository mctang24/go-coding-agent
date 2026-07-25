package agent

import (
	"context"
	"errors"
	"strings"
	"testing"

	"bare-agent/internal/tools"
)

func TestAgentCompact(t *testing.T) {
	history := []Message{
		{Role: "user", Content: "first question"},
		{Role: "assistant", ToolCalls: []ToolCall{{ID: "call_1", Name: "read_file", Arguments: `{"path":"a.go"}`}}},
		{Role: "tool", ToolResults: []ToolResult{{ToolCallID: "call_1", Content: "first result"}}},
		{Role: "assistant", Content: "first answer"},
		{Role: "user", Content: "second question"},
		{Role: "assistant", ToolCalls: []ToolCall{{ID: "call_2", Name: "read_file", Arguments: `{"path":"b.go"}`}}},
		{Role: "tool", ToolResults: []ToolResult{{ToolCallID: "call_2", Content: "second result"}}},
		{Role: "assistant", Content: "second answer"},
		{Role: "user", Content: "third question"},
		{Role: "assistant", Content: "third answer"},
	}
	model := &modelStub{
		responses:       []ModelResponse{{Message: Message{Role: "assistant", Content: "  " + validSummary() + "  "}}},
		estimatedTokens: 123,
	}
	runner := Agent{
		model:        model,
		instructions: "coding instructions",
		tools:        []tools.Tool{{Name: "read_file", Description: "read a file"}},
		messages:     history,
		tokenUsage:   999,
	}

	if err := runner.Compact(context.Background()); err != nil {
		t.Fatalf("Compact() error = %v", err)
	}

	if len(model.requests) != 1 {
		t.Fatalf("summary request count = %d, want 1", len(model.requests))
	}
	request := model.requests[0]
	if request.Instructions != summaryInstructions || len(request.Tools) != 0 {
		t.Fatalf("summary request = %#v", request)
	}
	if len(request.Messages) != 5 {
		t.Fatalf("summary message count = %d, want 5", len(request.Messages))
	}
	if request.Messages[1].ToolCalls[0].ID != "call_1" || request.Messages[2].ToolResults[0].ToolCallID != "call_1" {
		t.Fatalf("tool pair was not kept in summary input: %#v", request.Messages)
	}
	if request.Messages[4].Role != "user" || request.Messages[4].Content != initialSummaryPrompt {
		t.Fatalf("summary instruction message = %#v", request.Messages[4])
	}

	if len(runner.messages) != 7 {
		t.Fatalf("compacted message count = %d, want 7", len(runner.messages))
	}
	if runner.messages[0].Role != "user" || runner.messages[0].Content != summaryPrefix+validSummary() {
		t.Fatalf("summary message = %#v", runner.messages[0])
	}
	if runner.messages[1].Content != "second question" || runner.messages[2].ToolCalls[0].ID != "call_2" ||
		runner.messages[3].ToolResults[0].ToolCallID != "call_2" || runner.messages[6].Content != "third answer" {
		t.Fatalf("retained messages = %#v", runner.messages[1:])
	}
	if runner.tokenUsage != 123 {
		t.Fatalf("token usage = %d, want 123", runner.tokenUsage)
	}
	if model.estimateCalls != 1 {
		t.Fatalf("estimate calls = %d, want 1", model.estimateCalls)
	}
	estimated := model.estimateRequest
	if estimated.Instructions != "coding instructions" || len(estimated.Tools) != 1 || len(estimated.Messages) != 7 {
		t.Fatalf("estimated request = %#v", estimated)
	}
}

func TestAgentCompactIncludesExistingSummary(t *testing.T) {
	model := &modelStub{
		responses:       []ModelResponse{{Message: Message{Role: "assistant", Content: validSummary()}}},
		estimatedTokens: 50,
	}
	runner := Agent{
		model: model,
		messages: []Message{
			{Role: "user", Content: summaryPrefix + "existing summary"},
			{Role: "user", Content: "first question"},
			{Role: "assistant", Content: "first answer"},
			{Role: "user", Content: "second question"},
			{Role: "assistant", Content: "second answer"},
			{Role: "user", Content: "third question"},
			{Role: "assistant", Content: "third answer"},
		},
	}

	if err := runner.Compact(context.Background()); err != nil {
		t.Fatalf("Compact() error = %v", err)
	}
	request := model.requests[0]
	if len(request.Messages) != 4 || !strings.Contains(request.Messages[0].Content, "existing summary") {
		t.Fatalf("summary request messages = %#v", request.Messages)
	}
	if request.Messages[3].Content != updateSummaryPrompt {
		t.Fatalf("summary update instruction = %#v", request.Messages[3])
	}
}

func TestAgentCompactRetriesTransientFailure(t *testing.T) {
	model := &compactSequenceModel{
		errs:            []error{ErrRateLimit},
		responses:       []ModelResponse{{Message: Message{Role: "assistant", Content: validSummary()}}},
		estimatedTokens: 50,
	}
	runner := Agent{model: model, messages: compactableHistory()}

	if err := runner.Compact(context.Background()); err != nil {
		t.Fatalf("Compact() error = %v", err)
	}
	if len(model.requests) != 2 {
		t.Fatalf("summary request count = %d, want 2", len(model.requests))
	}
	if runner.tokenUsage != 50 {
		t.Fatalf("token usage = %d, want 50", runner.tokenUsage)
	}
}

func TestAgentCompactStopsAfterThreeTransientFailures(t *testing.T) {
	model := &compactSequenceModel{
		errs: []error{
			ErrRateLimit,
			ErrRateLimit,
			ErrRateLimit,
		},
	}
	history := compactableHistory()
	runner := Agent{model: model, messages: history, tokenUsage: 77}

	err := runner.Compact(context.Background())
	if err == nil || !errors.Is(err, ErrRateLimit) {
		t.Fatalf("Compact() error = %v, want final transient error", err)
	}
	if len(model.requests) != 3 || runner.tokenUsage != 77 || len(runner.messages) != len(history) {
		t.Fatalf("retry state = requests %d, token usage %d, messages %d", len(model.requests), runner.tokenUsage, len(runner.messages))
	}
}

func TestAgentCompactCancellationStopsRetry(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	model := &compactSequenceModel{
		errs:         []error{ErrRateLimit},
		cancelOnCall: cancel,
	}
	runner := Agent{model: model, messages: compactableHistory()}

	err := runner.Compact(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Compact() error = %v, want context canceled", err)
	}
	if len(model.requests) != 1 {
		t.Fatalf("summary request count = %d, want 1", len(model.requests))
	}
}

func TestSplitHistoryDoesNotCountSummaryAsUserTurn(t *testing.T) {
	messages := []Message{
		{Role: "user", Content: summaryPrefix + "existing summary"},
		{Role: "user", Content: "first question"},
		{Role: "assistant", Content: "first answer"},
		{Role: "user", Content: "second question"},
		{Role: "assistant", Content: "second answer"},
	}

	older, recent := splitHistory(messages)

	if len(older) != 1 || older[0].Content != summaryPrefix+"existing summary" {
		t.Fatalf("older messages = %#v", older)
	}
	if len(recent) != 4 || recent[0].Content != "first question" {
		t.Fatalf("recent messages = %#v", recent)
	}
}

func TestAgentCompactKeepsStateOnFailure(t *testing.T) {
	modelError := errors.New("model failed")
	estimateError := errors.New("estimate failed")
	tests := []struct {
		name    string
		model   Model
		wantErr string
	}{
		{name: "model error", model: errorModel{err: modelError}, wantErr: "model failed"},
		{
			name: "empty summary",
			model: &modelStub{
				responses: []ModelResponse{{Message: Message{Role: "assistant", Content: " \n "}}},
			},
			wantErr: "summary is empty",
		},
		{
			name: "tool call response",
			model: &modelStub{
				responses: []ModelResponse{{Message: Message{Role: "assistant", ToolCalls: []ToolCall{{ID: "call_1"}}}}},
			},
			wantErr: "contains tool calls",
		},
		{
			name: "missing summary section",
			model: &modelStub{
				responses: []ModelResponse{{Message: Message{Role: "assistant", Content: "ordinary summary"}}},
			},
			wantErr: "missing section",
		},
		{
			name: "estimate error",
			model: &modelStub{
				responses:   []ModelResponse{{Message: Message{Role: "assistant", Content: validSummary()}}},
				estimateErr: estimateError,
			},
			wantErr: "estimate compacted context",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			history := compactableHistory()
			runner := Agent{model: test.model, messages: history, tokenUsage: 77}

			err := runner.Compact(context.Background())
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("Compact() error = %v, want to contain %q", err, test.wantErr)
			}
			if runner.tokenUsage != 77 || len(runner.messages) != len(history) {
				t.Fatalf("state changed: token usage = %d, messages = %#v", runner.tokenUsage, runner.messages)
			}
			for index := range history {
				if runner.messages[index].Role != history[index].Role || runner.messages[index].Content != history[index].Content {
					t.Fatalf("message %d changed: %#v", index, runner.messages[index])
				}
			}
		})
	}
}

func TestAgentCompactSkipsInsufficientHistory(t *testing.T) {
	model := &modelStub{}
	runner := Agent{
		model: model,
		messages: []Message{
			{Role: "user", Content: "first question"},
			{Role: "assistant", Content: "first answer"},
			{Role: "user", Content: "second question"},
			{Role: "assistant", Content: "second answer"},
		},
		tokenUsage: 20,
	}

	err := runner.Compact(context.Background())
	if err != nil {
		t.Fatalf("Compact() error = %v, want nil", err)
	}
	if len(model.requests) != 0 || model.estimateCalls != 0 || runner.tokenUsage != 20 {
		t.Fatalf("state or model calls changed: agent = %#v, model = %#v", runner, model)
	}
}

func compactableHistory() []Message {
	return []Message{
		{Role: "user", Content: "first question"},
		{Role: "assistant", Content: "first answer"},
		{Role: "user", Content: "second question"},
		{Role: "assistant", Content: "second answer"},
		{Role: "user", Content: "third question"},
		{Role: "assistant", Content: "third answer"},
	}
}

func validSummary() string {
	return strings.Join(summarySections, "\n")
}

type compactSequenceModel struct {
	errs            []error
	responses       []ModelResponse
	requests        []ModelRequest
	estimatedTokens int
	cancelOnCall    context.CancelFunc
}

func (model *compactSequenceModel) GenerateResponse(_ context.Context, request ModelRequest) (ModelStream, error) {
	model.requests = append(model.requests, request)
	if model.cancelOnCall != nil {
		model.cancelOnCall()
		model.cancelOnCall = nil
	}
	if len(model.errs) > 0 {
		err := model.errs[0]
		model.errs = model.errs[1:]
		if err != nil {
			return nil, err
		}
	}
	response := model.responses[0]
	model.responses = model.responses[1:]
	return &stubModelStream{response: response}, nil
}

func (model *compactSequenceModel) EstimateRequestTokens(ModelRequest) (int, error) {
	return model.estimatedTokens, nil
}
