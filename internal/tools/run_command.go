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

func (workspaceTools *WorkspaceTools) executeRunCommand(ctx context.Context, root, arguments string) (string, error) {
	var input CommandInput
	if err := decodeArguments(arguments, &input); err != nil {
		return "", fmt.Errorf("execute run_command: decode arguments: %w", err)
	}
	result, err := workspaceTools.runCommand(ctx, root, input)
	if err != nil {
		return "", err
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return "", fmt.Errorf("execute run_command: encode result: %w", err)
	}
	return string(encoded), nil
}

func (workspaceTools *WorkspaceTools) runCommand(ctx context.Context, root string, input CommandInput) (CommandResult, error) {
	if input.Command == "" {
		return CommandResult{}, fmt.Errorf("execute run_command: command is empty")
	}
	if input.Args == nil {
		return CommandResult{}, fmt.Errorf("execute run_command: args is required")
	}
	if workspaceTools.commandApprover == nil {
		return CommandResult{}, fmt.Errorf("execute run_command: command approval is not configured")
	}
	approved, err := workspaceTools.commandApprover(ctx, CommandRequest{Tool: "run_command", Command: input.Command, Args: input.Args})
	if err != nil {
		return CommandResult{}, fmt.Errorf("execute run_command: request approval: %w", err)
	}
	if !approved {
		return CommandResult{}, fmt.Errorf("execute run_command: user denied command")
	}

	commandCtx, cancel := context.WithTimeout(ctx, workspaceTools.commandTimeout)
	defer cancel()
	command := exec.CommandContext(commandCtx, input.Command, input.Args...)
	command.Dir = root
	var stdout, stderr limitedOutput
	command.Stdout = &stdout
	command.Stderr = &stderr
	runErr := command.Run()
	result := CommandResult{
		Stdout: stdout.String(),
		Stderr: stderr.String(),
	}

	if errors.Is(commandCtx.Err(), context.DeadlineExceeded) {
		return CommandResult{}, fmt.Errorf("execute run_command: command timed out after %s", workspaceTools.commandTimeout)
	}
	if commandCtx.Err() != nil {
		return CommandResult{}, fmt.Errorf("execute run_command: command cancelled: %w", commandCtx.Err())
	}
	if runErr == nil {
		return result, nil
	}
	if exitError, ok := runErr.(*exec.ExitError); ok {
		result.ExitCode = exitError.ExitCode()
		return result, nil
	}
	return CommandResult{}, fmt.Errorf("execute run_command: run command: %w", runErr)
}
