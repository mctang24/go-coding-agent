package main

import (
	"go-coding-agent/internal/agent"
	"strings"
	"testing"
)

func TestParseArgs(t *testing.T) {
	parsed, err := parseArgs([]string{"-root", "project", "-trace", "trace.jsonl", "-session", "session-id", "inspect", "the", "code"})
	if err != nil {
		t.Fatalf("parseArgs() error = %v", err)
	}
	if parsed.root != "project" || parsed.tracePath != "trace.jsonl" || parsed.sessionID != "session-id" || parsed.task != "inspect the code" {
		t.Fatalf("parseArgs() = %#v", parsed)
	}
}

func TestParseArgsWithoutTask(t *testing.T) {
	parsed, err := parseArgs(nil)
	if err != nil {
		t.Fatalf("parseArgs() error = %v", err)
	}
	if parsed.root != "." || parsed.tracePath != "" || parsed.sessionID != "" || parsed.task != "" {
		t.Fatalf("parseArgs() = %#v", parsed)
	}
}

func TestParseArgsErrors(t *testing.T) {
	for _, args := range [][]string{{"-unknown"}} {
		if _, err := parseArgs(args); err == nil || !strings.Contains(err.Error(), "parse arguments") {
			t.Fatalf("parseArgs(%q) error = %v", args, err)
		}
	}
}

func TestSystemPromptRequiresVerification(t *testing.T) {
	for _, requirement := range []string{
		"任务完成时必须单独调用 finish_task",
		"在 finish_task 中提供 command 和 args",
		"禁止先用 run_command 执行同一检查",
		"run_command 只用于任务尚未完成时的诊断和中间操作",
	} {
		if !strings.Contains(systemPrompt, requirement) {
			t.Errorf("systemPrompt missing %q", requirement)
		}
	}
}

func TestExitCode(t *testing.T) {
	tests := []struct {
		status agent.RunStatus
		want   int
	}{
		{status: agent.RunStatusSuccess, want: 0},
		{status: agent.RunStatusIncomplete, want: 1},
		{status: agent.RunStatusInterrupted, want: 1},
		{status: agent.RunStatusError, want: 2},
	}
	for _, test := range tests {
		if got := exitCode(test.status); got != test.want {
			t.Errorf("exitCode(%q) = %d, want %d", test.status, got, test.want)
		}
	}
}
