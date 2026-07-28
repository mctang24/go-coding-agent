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
	first, err := newTraceID("session")
	if err != nil {
		t.Fatalf("newTraceID() error = %v", err)
	}
	second, err := newTraceID("run")
	if err != nil {
		t.Fatalf("newTraceID() error = %v", err)
	}
	if len(first) != 40 || first[:8] != "session_" || len(second) != 36 || second[:4] != "run_" {
		t.Fatalf("trace IDs = %q, %q", first, second)
	}
	if _, err := hex.DecodeString(first[8:]); err != nil {
		t.Fatalf("trace ID %q is not hexadecimal: %v", first, err)
	}
	if _, err := newTraceID(""); err == nil {
		t.Fatal("newTraceID() error = nil, want empty prefix error")
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
