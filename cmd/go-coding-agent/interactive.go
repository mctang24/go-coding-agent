package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"go-coding-agent/internal/agent"
	"go-coding-agent/internal/tools"
	"io"
	"strings"
)

func runTask(ctx context.Context, runner *agent.Agent, task string, output io.Writer) (agent.RunResult, error) {
	return runner.Run(ctx, task, func(delta string) error {
		_, err := io.WriteString(output, delta)
		return err
	})
}

// runInteractive runs an in-memory conversation until the user exits.
func runInteractive(ctx context.Context, runner *agent.Agent, scanner *bufio.Scanner, output, errorOutput io.Writer) error {
	for {
		fmt.Fprint(output, "> ")
		if !scanner.Scan() {
			return scanner.Err()
		}
		task := strings.TrimSpace(scanner.Text())
		switch task {
		case "":
			continue
		case "/exit":
			return nil
		case "/new":
			if err := runner.Reset(); err != nil {
				return err
			}
			fmt.Fprintln(output, "new conversation")
			continue
		case "/compact":
			if err := runner.Compact(ctx); err != nil {
				fmt.Fprintln(errorOutput, err)
				continue
			}
			fmt.Fprintln(output, "conversation compacted")
			continue
		}

		result, err := runTask(ctx, runner, task, output)
		fmt.Fprintln(output)
		if err != nil {
			fmt.Fprintln(errorOutput, err)
			continue
		}
		if result.Status == agent.RunStatusIncomplete {
			fmt.Fprintln(errorOutput, "task incomplete: changes are not verified")
		}
	}
}

func newScannerCommandApprover(scanner *bufio.Scanner, output io.Writer) tools.CommandApprover {
	return func(_ context.Context, request tools.CommandRequest) (bool, error) {
		command, err := json.Marshal(append([]string{request.Command}, request.Args...))
		if err != nil {
			return false, fmt.Errorf("format command approval: %w", err)
		}
		return scanApproval(scanner, output, fmt.Sprintf("允许 %s 执行 %s？[Y/n] ", request.Tool, command))
	}
}

func newScannerWriteApprover(scanner *bufio.Scanner, output io.Writer) tools.WriteApprover {
	return func(_ context.Context, request tools.WriteRequest) (bool, error) {
		return scanApproval(scanner, output, fmt.Sprintf("允许 %s 写入 %q？[Y/n] ", request.Tool, request.Path))
	}
}

func scanApproval(scanner *bufio.Scanner, output io.Writer, prompt string) (bool, error) {
	for {
		fmt.Fprint(output, prompt)
		if !scanner.Scan() {
			return false, scanner.Err()
		}
		switch strings.TrimSpace(strings.ToLower(scanner.Text())) {
		case "", "y", "yes":
			return true, nil
		case "n", "no":
			return false, nil
		}
	}
}
