package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRunCommand(t *testing.T) {
	root := t.TempDir()
	workspaceTools := NewWorkspaceTools()
	workspaceTools.SetCommandApprover(func(_ context.Context, request CommandRequest) (bool, error) {
		if request.Tool != "run_command" || request.Command != os.Args[0] || len(request.Args) == 0 {
			t.Fatalf("approval request = %#v", request)
		}
		return true, nil
	})
	run := findTool(t, workspaceTools, "run_command")

	output, err := run.Execute(context.Background(), root, helperCommandArguments("output"))
	if err != nil {
		t.Fatalf("run_command error = %v", err)
	}
	var result CommandResult
	if err := json.Unmarshal([]byte(output), &result); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if result.ExitCode != 0 || result.Stdout != "stdout" || result.Stderr != "stderr" {
		t.Fatalf("result = %#v", result)
	}
}

func TestRunCommandUsesRootAsWorkingDirectory(t *testing.T) {
	root := t.TempDir()
	workspaceTools := NewWorkspaceTools()
	workspaceTools.SetCommandApprover(func(context.Context, CommandRequest) (bool, error) { return true, nil })
	run := findTool(t, workspaceTools, "run_command")

	output, err := run.Execute(context.Background(), root, helperCommandArguments("working_directory"))
	if err != nil {
		t.Fatalf("run_command error = %v", err)
	}
	var result CommandResult
	if err := json.Unmarshal([]byte(output), &result); err != nil {
		t.Fatal(err)
	}
	if result.Stdout != root {
		t.Fatalf("working directory = %q, want %q", result.Stdout, root)
	}
}

func TestRunCommandReturnsNonzeroExit(t *testing.T) {
	workspaceTools := NewWorkspaceTools()
	workspaceTools.SetCommandApprover(func(context.Context, CommandRequest) (bool, error) { return true, nil })
	run := findTool(t, workspaceTools, "run_command")

	output, err := run.Execute(context.Background(), t.TempDir(), helperCommandArguments("exit"))
	if err != nil {
		t.Fatalf("run_command error = %v", err)
	}
	var result CommandResult
	if err := json.Unmarshal([]byte(output), &result); err != nil {
		t.Fatal(err)
	}
	if result.ExitCode != 7 || result.Stderr != "failed" {
		t.Fatalf("result = %#v", result)
	}
}

func TestRunCommandTruncatesOutput(t *testing.T) {
	workspaceTools := NewWorkspaceTools()
	workspaceTools.SetCommandApprover(func(context.Context, CommandRequest) (bool, error) { return true, nil })
	run := findTool(t, workspaceTools, "run_command")

	output, err := run.Execute(context.Background(), t.TempDir(), helperCommandArguments("large_output"))
	if err != nil {
		t.Fatalf("run_command error = %v", err)
	}
	var result CommandResult
	if err := json.Unmarshal([]byte(output), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Stdout) <= toolOutputLimit || len(result.Stderr) <= toolOutputLimit ||
		!strings.HasPrefix(result.Stdout, strings.Repeat("o", 100)) ||
		!strings.HasSuffix(result.Stdout, "stdout end") ||
		!strings.Contains(result.Stdout, "[... truncated ") ||
		!strings.HasSuffix(result.Stderr, "stderr end") {
		t.Fatalf("stdout length = %d, stderr length = %d, result = %#v", len(result.Stdout), len(result.Stderr), result)
	}
}

func TestRunCommandApproval(t *testing.T) {
	runWithoutApprover := findTool(t, NewWorkspaceTools(), "run_command")
	if _, err := runWithoutApprover.Execute(context.Background(), t.TempDir(), helperCommandArguments("output")); err == nil || !strings.Contains(err.Error(), "approval is not configured") {
		t.Fatalf("missing approval error = %v", err)
	}

	workspaceTools := NewWorkspaceTools()
	workspaceTools.SetCommandApprover(func(context.Context, CommandRequest) (bool, error) { return false, nil })
	run := findTool(t, workspaceTools, "run_command")
	if _, err := run.Execute(context.Background(), t.TempDir(), helperCommandArguments("output")); err == nil || !strings.Contains(err.Error(), "user denied") {
		t.Fatalf("denied error = %v", err)
	}
}

func TestRunCommandTimeout(t *testing.T) {
	workspaceTools := NewWorkspaceTools()
	workspaceTools.commandTimeout = 20 * time.Millisecond
	workspaceTools.SetCommandApprover(func(context.Context, CommandRequest) (bool, error) { return true, nil })
	run := findTool(t, workspaceTools, "run_command")

	_, err := run.Execute(context.Background(), t.TempDir(), helperCommandArguments("sleep"))
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("timeout error = %v", err)
	}
	var executionErr *CommandExecutionError
	if !errors.As(err, &executionErr) {
		t.Fatalf("timeout error type = %T, want *CommandExecutionError", err)
	}
}

func TestVerifyCommand(t *testing.T) {
	workspaceTools := NewWorkspaceTools()
	workspaceTools.SetCommandApprover(func(_ context.Context, request CommandRequest) (bool, error) {
		if request.Tool != "verify_command" {
			t.Fatalf("approval tool = %q, want verify_command", request.Tool)
		}
		return true, nil
	})
	verify := findTool(t, workspaceTools, "verify_command")

	output, err := verify.Execute(context.Background(), t.TempDir(), helperCommandArguments("exit"))
	if err != nil {
		t.Fatalf("verify_command error = %v", err)
	}
	var result CommandResult
	if err := json.Unmarshal([]byte(output), &result); err != nil {
		t.Fatal(err)
	}
	if result.ExitCode != 7 || result.Stderr != "failed" {
		t.Fatalf("result = %#v", result)
	}
}

func TestRunCommandValidatesArguments(t *testing.T) {
	workspaceTools := NewWorkspaceTools()
	workspaceTools.SetCommandApprover(func(context.Context, CommandRequest) (bool, error) {
		t.Fatal("approver called for invalid arguments")
		return false, nil
	})
	run := findTool(t, workspaceTools, "run_command")
	tests := []struct {
		arguments string
		want      string
	}{
		{arguments: `{"command":"","args":[]}`, want: "command is empty"},
		{arguments: `{"command":"go"}`, want: "args is required"},
		{arguments: `{"command":"go","args":[],"shell":true}`, want: "unknown field"},
	}
	for _, test := range tests {
		if _, err := run.Execute(context.Background(), t.TempDir(), test.arguments); err == nil || !strings.Contains(err.Error(), test.want) {
			t.Errorf("arguments %s error = %v, want %q", test.arguments, err, test.want)
		}
	}
}

func helperCommandArguments(mode string) string {
	arguments, err := json.Marshal(CommandInput{
		Command: os.Args[0],
		Args:    []string{"-test.run=TestRunCommandHelperProcess", "--", mode},
	})
	if err != nil {
		panic(err)
	}
	return string(arguments)
}

func TestRunCommandHelperProcess(t *testing.T) {
	if len(os.Args) < 2 || os.Args[len(os.Args)-2] != "--" {
		return
	}
	switch os.Args[len(os.Args)-1] {
	case "output":
		fmt.Fprint(os.Stdout, "stdout")
		fmt.Fprint(os.Stderr, "stderr")
	case "working_directory":
		workingDirectory, err := os.Getwd()
		if err != nil {
			os.Exit(2)
		}
		fmt.Fprint(os.Stdout, filepath.Clean(workingDirectory))
	case "exit":
		fmt.Fprint(os.Stderr, "failed")
		os.Exit(7)
	case "large_output":
		fmt.Fprint(os.Stdout, strings.Repeat("o", toolOutputLimit+1))
		fmt.Fprint(os.Stdout, "stdout end")
		fmt.Fprint(os.Stderr, strings.Repeat("e", toolOutputLimit+1))
		fmt.Fprint(os.Stderr, "stderr end")
	case "sleep":
		time.Sleep(time.Second)
	default:
		os.Exit(2)
	}
	os.Exit(0)
}
