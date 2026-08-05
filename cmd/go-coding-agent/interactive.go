package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/term"

	"go-coding-agent/internal/agent"
)

type cliInput struct {
	scanner *bufio.Scanner
	fd      int
}

func newCLIInput(file *os.File) (cliInput, error) {
	fd := int(file.Fd())
	if !term.IsTerminal(fd) {
		return cliInput{}, fmt.Errorf("stdin is not a TTY")
	}
	return cliInput{scanner: bufio.NewScanner(file), fd: fd}, nil
}

type newlineTrackingWriter struct {
	io.Writer
	needsNewline bool
}

func (output *newlineTrackingWriter) Write(content []byte) (int, error) {
	written, err := output.Writer.Write(content)
	if written > 0 {
		output.needsNewline = content[written-1] != '\n'
	}
	return written, err
}

func runTask(ctx context.Context, runner *agent.Agent, task string, output io.Writer) (agent.RunResult, error) {
	return runner.Run(ctx, task, func(delta string) error {
		_, err := io.WriteString(output, delta)
		return err
	})
}

// runInteractive runs an in-memory conversation until the user exits.
func runInteractive(ctx context.Context, runner *agent.Agent, input cliInput, output io.Writer) error {
	for {
		fmt.Fprint(output, "> ")
		scanned := make(chan bool, 1)
		go func() {
			scanned <- input.scanner.Scan()
		}()
		select {
		case <-ctx.Done():
			if errors.Is(context.Cause(ctx), agent.ErrRunInterrupted) {
				fmt.Fprintln(output)
			}
			return context.Cause(ctx)
		case ok := <-scanned:
			if !ok {
				return input.scanner.Err()
			}
		}
		task := strings.TrimSpace(input.scanner.Text())
		switch task {
		case "":
			continue
		case "/exit":
			return nil
		case "/new":
			endedSessionID := runner.SessionID()
			if err := runner.Reset(); err != nil {
				fmt.Fprintln(output, err)
				continue
			}
			printSessionID(output, endedSessionID)
			fmt.Fprintln(output, "new conversation")
			continue
		case "/compact":
			if err := runner.Compact(ctx); err != nil {
				if ctx.Err() != nil {
					return context.Cause(ctx)
				}
				fmt.Fprintln(output, err)
				continue
			}
			fmt.Fprintln(output, "conversation compacted")
			continue
		}

		runOutput := &newlineTrackingWriter{Writer: output}
		result, err := runTaskWithInterrupt(ctx, runner, task, input.fd, runOutput)
		if runOutput.needsNewline {
			fmt.Fprintln(output)
		}
		if ctx.Err() != nil {
			return context.Cause(ctx)
		}
		if err != nil {
			fmt.Fprintln(output, err)
			continue
		}
		switch result.Status {
		case agent.RunStatusInterrupted:
			fmt.Fprintln(output, "interrupted")
		case agent.RunStatusIncomplete:
			fmt.Fprintln(output, "task incomplete: completion was not confirmed or changes are not verified")
		}
	}
}

func printSessionID(output io.Writer, sessionID string) {
	if sessionID != "" {
		fmt.Fprintf(output, "sessionId: %s\n", sessionID)
	}
}
