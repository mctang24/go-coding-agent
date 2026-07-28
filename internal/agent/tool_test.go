package agent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go-coding-agent/internal/tools"
)

func TestAgentExecuteTool(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "file.txt"), []byte("hello"), 0o600); err != nil {
		t.Fatalf("write test file: %v", err)
	}
	agent := Agent{root: root, tools: tools.NewWorkspaceTools().Definitions()}

	result, err := agent.executeTool(context.Background(), "read_file", `{"path":"file.txt"}`)
	if err != nil {
		t.Fatalf("executeTool() error = %v", err)
	}
	if result != "   1 | hello" {
		t.Errorf("executeTool() = %q, want numbered content", result)
	}
}

func TestAgentExecuteToolErrors(t *testing.T) {
	failingTool := tools.Tool{
		Name: "failing_tool",
		Execute: func(context.Context, string, string) (string, error) {
			return "", errors.New("failed")
		},
	}
	tests := []struct {
		name     string
		tools    []tools.Tool
		toolName string
		wantErr  string
	}{
		{name: "empty name", wantErr: "name is empty"},
		{name: "unknown tool", toolName: "unknown", wantErr: "not registered"},
		{name: "nil execute function", tools: []tools.Tool{{Name: "broken"}}, toolName: "broken", wantErr: "execute function is nil"},
		{name: "tool error", tools: []tools.Tool{failingTool}, toolName: "failing_tool", wantErr: "failed"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			agent := Agent{root: t.TempDir(), tools: tt.tools}
			_, err := agent.executeTool(context.Background(), tt.toolName, `{}`)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("executeTool() error = %v, want to contain %q", err, tt.wantErr)
			}
		})
	}
}

func TestAgentExecuteToolCall(t *testing.T) {
	agent := Agent{root: t.TempDir(), tools: []tools.Tool{{
		Name: "echo",
		Execute: func(context.Context, string, string) (string, error) {
			return "done", nil
		},
	}}}

	result, err := agent.executeToolCall(context.Background(), ToolCall{ID: "call_1", Name: "echo", Arguments: `{}`})
	if err != nil {
		t.Fatalf("executeToolCall() error = %v", err)
	}
	if result.ToolCallID != "call_1" || result.Content != "done" || result.IsError {
		t.Fatalf("executeToolCall() = %#v", result)
	}
}

func TestAgentExecuteToolCallErrors(t *testing.T) {
	agent := Agent{root: t.TempDir()}

	if _, err := agent.executeToolCall(context.Background(), ToolCall{Name: "unknown"}); err == nil || !strings.Contains(err.Error(), "ID is empty") {
		t.Fatalf("executeToolCall() empty ID error = %v", err)
	}

	result, err := agent.executeToolCall(context.Background(), ToolCall{ID: "call_1", Name: "unknown", Arguments: `{}`})
	if err != nil {
		t.Fatalf("executeToolCall() tool error = %v", err)
	}
	if !result.IsError || result.ToolCallID != "call_1" || !strings.Contains(result.Content, "not registered") {
		t.Fatalf("executeToolCall() tool result = %#v", result)
	}

}
