package main

import (
	"go-coding-agent/internal/agent"
	"strings"
	"testing"
)

func TestParseArgs(t *testing.T) {
	parsed, err := parseArgs([]string{"-root", "project", "-trace", "trace.jsonl", "inspect", "the", "code"})
	if err != nil {
		t.Fatalf("parseArgs() error = %v", err)
	}
	if parsed.root != "project" || parsed.tracePath != "trace.jsonl" || parsed.task != "inspect the code" {
		t.Fatalf("parseArgs() = %#v", parsed)
	}
}

func TestParseArgsWithoutTask(t *testing.T) {
	parsed, err := parseArgs(nil)
	if err != nil {
		t.Fatalf("parseArgs() error = %v", err)
	}
	if parsed.root != "." || parsed.tracePath != "" || parsed.task != "" {
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
		"最终回答前必须调用 verify_command",
		"完成性检查必须使用 verify_command",
		"不能使用 run_command",
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
		{status: agent.RunStatusError, want: 2},
	}
	for _, test := range tests {
		if got := exitCode(test.status); got != test.want {
			t.Errorf("exitCode(%q) = %d, want %d", test.status, got, test.want)
		}
	}
}
