package agent

import (
	"context"
	"encoding/hex"
	"errors"
	"go-coding-agent/internal/trace"
	"path/filepath"
	"strings"
	"testing"
)

func TestNewTraceID(t *testing.T) {
	first, err := newRunID()
	if err != nil {
		t.Fatalf("newRunID() error = %v", err)
	}
	second, err := newRunID()
	if err != nil {
		t.Fatalf("newRunID() error = %v", err)
	}
	if len(first) != 36 || first[:4] != "run_" || len(second) != 36 || second[:4] != "run_" || first == second {
		t.Fatalf("run IDs = %q, %q", first, second)
	}
	if _, err := hex.DecodeString(first[4:]); err != nil {
		t.Fatalf("run ID %q is not hexadecimal: %v", first, err)
	}
}

func TestTraceErrorsDoNotReplaceBusinessResults(t *testing.T) {
	current := &runTrace{writer: &trace.Writer{Path: filepath.Join(t.TempDir(), "missing", "trace.jsonl")}}
	model := &modelStub{responses: []ModelResponse{{Message: Message{Role: "assistant", Content: "done"}}}}
	agent := Agent{model: model}

	response, err := agent.callModel(context.Background(), ModelRequest{}, nil, current, 1)
	if err != nil || response.Message.Content != "done" {
		t.Fatalf("callModel() = %#v, %v", response, err)
	}

	toolResult, err := agent.callTool(context.Background(), ToolCall{ID: "call_1", Name: "missing"}, current, 1)
	if err != nil || !toolResult.IsError {
		t.Fatalf("callTool() = %#v, %v", toolResult, err)
	}

	_, err = agent.callTool(context.Background(), ToolCall{}, current, 1)
	if err == nil || !strings.Contains(err.Error(), "ID is empty") {
		t.Fatalf("callTool() error = %v", err)
	}

	modelError := errors.New("model failed")
	agent.model = errorModel{err: modelError}
	_, err = agent.callModel(context.Background(), ModelRequest{}, nil, current, 2)
	if !errors.Is(err, modelError) {
		t.Fatalf("callModel() error = %v", err)
	}
}
