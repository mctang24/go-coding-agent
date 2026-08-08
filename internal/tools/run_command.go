package tools

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"time"

	"github.com/goccy/go-json"
)

const (
	defaultCommandTimeout = 60 * time.Second
)

type CommandInput struct {
	Command string   `json:"command"`
	Args    []string `json:"args"`
}

type CommandResult struct {
	ExitCode int    `json:"exit_code"`
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
}

// CommandExecutionError reports a failure after the command process started.
type CommandExecutionError struct {
	Err error
}

func (err *CommandExecutionError) Error() string {
	return err.Err.Error()
}

func (err *CommandExecutionError) Unwrap() error {
	return err.Err
}

func (workspaceTools *WorkspaceTools) executeRunCommand(ctx context.Context, root, arguments string) (string, error) {
	var input CommandInput
	if err := decodeArguments(arguments, &input); err != nil {
		return "", fmt.Errorf("execute run_command: decode arguments: %w", err)
	}
	result, err := workspaceTools.runCommand(ctx, root, "run_command", input)
	if err != nil {
		return "", err
	}
	return encodeResult("run_command", result)
}

func (workspaceTools *WorkspaceTools) runCommand(ctx context.Context, root, toolName string, input CommandInput) (CommandResult, error) {
	if input.Command == "" {
		return CommandResult{}, fmt.Errorf("execute %s: command is empty", toolName)
	}
	if input.Args == nil {
		return CommandResult{}, fmt.Errorf("execute %s: args is required", toolName)
	}
	if workspaceTools.commandApprover == nil {
		return CommandResult{}, fmt.Errorf("execute %s: command approval is not configured", toolName)
	}
	approved, err := workspaceTools.commandApprover(ctx, CommandRequest{Tool: toolName, Command: input.Command, Args: input.Args})
	if err != nil {
		return CommandResult{}, fmt.Errorf("execute %s: request approval: %w", toolName, err)
	}
	if !approved {
		return CommandResult{}, fmt.Errorf("execute %s: user denied command", toolName)
	}

	commandCtx, cancel := context.WithTimeout(ctx, workspaceTools.commandTimeout)
	defer cancel()
	command := exec.CommandContext(commandCtx, input.Command, input.Args...)
	command.Dir = root
	var stdout, stderr limitedOutput
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Start(); err != nil {
		return CommandResult{}, fmt.Errorf("execute %s: start command: %w", toolName, err)
	}
	waitErr := command.Wait()
	result := CommandResult{
		Stdout: stdout.String(),
		Stderr: stderr.String(),
	}

	exitError, isExitError := waitErr.(*exec.ExitError)
	switch {
	case errors.Is(commandCtx.Err(), context.DeadlineExceeded):
		return CommandResult{}, &CommandExecutionError{Err: fmt.Errorf("execute %s: command timed out after %s", toolName, workspaceTools.commandTimeout)}
	case commandCtx.Err() != nil:
		return CommandResult{}, &CommandExecutionError{Err: fmt.Errorf("execute %s: command cancelled: %w", toolName, commandCtx.Err())}
	case waitErr == nil:
	case isExitError:
		result.ExitCode = exitError.ExitCode()
	default:
		return CommandResult{}, &CommandExecutionError{Err: fmt.Errorf("execute %s: wait for command: %w", toolName, waitErr)}
	}
	return result, nil
}

func encodeResult(toolName string, result any) (string, error) {
	encoded, err := json.Marshal(result)
	if err != nil {
		return "", fmt.Errorf("execute %s: encode result: %w", toolName, err)
	}
	return string(encoded), nil
}
