package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/goccy/go-json"

	"go-coding-agent/internal/tools"
)

type verificationFact struct {
	Tool     string `json:"tool"`
	Command  string `json:"command"`
	ExitCode *int   `json:"exitCode,omitempty"`
	Error    string `json:"error,omitempty"`
}

// executeTool finds and executes a tool requested by the model.
func (agent *Agent) executeTool(ctx context.Context, name, arguments string) (string, error) {
	if name == "" {
		return "", fmt.Errorf("agent execute tool: name is empty")
	}
	for _, tool := range agent.tools {
		if tool.Name != name {
			continue
		}
		if tool.Execute == nil {
			return "", fmt.Errorf("agent execute tool %q: execute function is nil", name)
		}

		result, err := tool.Execute(ctx, agent.root, arguments)
		if err != nil {
			return "", fmt.Errorf("agent execute tool %q: %w", name, err)
		}
		return result, nil
	}

	return "", fmt.Errorf("agent execute tool %q: not registered", name)
}

// executeToolCall executes one model tool call and builds its result.
func (agent *Agent) executeToolCall(ctx context.Context, call ToolCall) (ToolResult, error) {
	if call.ID == "" {
		return ToolResult{}, fmt.Errorf("agent execute tool call: ID is empty")
	}

	result, err := agent.executeTool(ctx, call.Name, call.Arguments)
	agent.recordToolExecution(call, result, err)
	if err != nil {
		if isRunInterrupted(ctx) {
			return ToolResult{ToolCallID: call.ID, Content: "aborted by user", IsError: true}, nil
		}
		return ToolResult{ToolCallID: call.ID, Content: err.Error(), IsError: true}, nil
	}
	if call.Name == "finish_task" {
		var finishResult tools.FinishTaskResult
		if err := json.Unmarshal([]byte(result), &finishResult); err != nil {
			return ToolResult{}, fmt.Errorf("agent execute finish_task: decode result: %w", err)
		}
		switch {
		case finishResult.Verification == nil && agent.hasUnverifiedChange:
			return ToolResult{
				ToolCallID: call.ID,
				Content:    "finish_task requires command and args because changes are not verified",
				IsError:    true,
			}, nil
		case finishResult.Verification != nil && finishResult.Verification.ExitCode != 0:
			return ToolResult{ToolCallID: call.ID, Content: result, IsError: true}, nil
		}
	}

	return ToolResult{ToolCallID: call.ID, Content: result}, nil
}

func (agent *Agent) recordToolExecution(call ToolCall, output string, executionErr error) {
	switch call.Name {
	case "edit_file", "write_file":
		if executionErr == nil {
			agent.invalidateVerification()
		}
	case "run_command":
		if executionErr == nil || failedAfterCommandStart(executionErr) {
			agent.invalidateVerification()
		}
	case "finish_task":
		agent.recordVerification(call.Arguments, output, executionErr)
	}
}

func (agent *Agent) invalidateVerification() {
	agent.hasUnverifiedChange = true
	agent.lastVerification = nil
}

func (agent *Agent) recordVerification(arguments, output string, executionErr error) {
	if executionErr != nil && !failedAfterCommandStart(executionErr) {
		return
	}

	var input tools.FinishTaskInput
	if err := json.Unmarshal([]byte(arguments), &input); err != nil {
		return
	}
	if input.Command == "" {
		return
	}
	fact := &verificationFact{
		Tool:    "finish_task",
		Command: strings.Join(append([]string{input.Command}, input.Args...), " "),
	}
	if executionErr != nil {
		fact.Error = executionErr.Error()
		agent.hasUnverifiedChange = true
		agent.lastVerification = fact
		return
	}

	var result tools.FinishTaskResult
	if err := json.Unmarshal([]byte(output), &result); err != nil {
		return
	}
	if result.Verification == nil {
		return
	}
	fact.ExitCode = &result.Verification.ExitCode
	agent.hasUnverifiedChange = result.Verification.ExitCode != 0
	agent.lastVerification = fact
}

func failedAfterCommandStart(err error) bool {
	var executionErr *tools.CommandExecutionError
	return errors.As(err, &executionErr)
}
