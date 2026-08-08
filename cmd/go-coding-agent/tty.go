package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/goccy/go-json"
	"golang.org/x/sys/unix"
	"golang.org/x/term"

	"go-coding-agent/internal/agent"
	"go-coding-agent/internal/tools"
)

const (
	// ttyPollTimeoutMS limits how long the input loop waits before checking cancellation again.
	ttyPollTimeoutMS = 25
	// ttyCarriageReturn and ttyLineFeed are the two Enter bytes emitted by terminals.
	ttyCarriageReturn byte = '\r'
	// ttyLineFeed submits input on terminals that emit line feed for Enter.
	ttyLineFeed byte = '\n'
	// ttyEscape is the byte emitted by the Esc key.
	ttyEscape byte = 0x1b
)

type ttyApproval struct {
	prompt string
	result chan bool
}

type ttyRunInput struct {
	fd        int
	interrupt context.CancelCauseFunc
	approvals chan ttyApproval
}

type workingOutput struct {
	output io.Writer
	stop   func()
}

func (output workingOutput) Write(content []byte) (int, error) {
	output.stop()
	return output.output.Write(content)
}

func formatWorkingStatus(seconds int) string {
	if seconds < 60 {
		return fmt.Sprintf("working (%ds • esc to interrupt)", seconds)
	}
	return fmt.Sprintf("working (%dmin %ds • esc to interrupt)", seconds/60, seconds%60)
}

func startWorkingStatus(output io.Writer) func() {
	startedAt := time.Now()
	stop := make(chan struct{})
	done := make(chan struct{})
	var stopOnce sync.Once
	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		defer close(done)
		for {
			select {
			case now := <-ticker.C:
				seconds := int(now.Sub(startedAt).Seconds())
				_, _ = fmt.Fprintf(output, "\r%s", formatWorkingStatus(seconds))
			case <-stop:
				return
			}
		}
	}()
	return func() {
		stopOnce.Do(func() {
			close(stop)
			<-done
			// Clear the working status and start the response on the next line.
			_, _ = fmt.Fprint(output, "\r\x1b[2K\n")
		})
	}
}

func runTaskWithInterrupt(ctx context.Context, runner *agent.Agent, task string, fd int, output io.Writer) (agent.RunResult, error) {
	state, err := term.MakeRaw(fd)
	if err != nil {
		return agent.RunResult{}, fmt.Errorf("enable Esc interrupt: %w", err)
	}
	restored := false
	defer func() {
		if !restored {
			_ = term.Restore(fd, state)
		}
	}()
	if err := enableTTYSignalsAndOutput(fd); err != nil {
		return agent.RunResult{}, fmt.Errorf("preserve terminal signal and output processing: %w", err)
	}
	runCtx, interrupt := context.WithCancelCause(ctx)
	input := &ttyRunInput{
		fd:        fd,
		interrupt: interrupt,
		approvals: make(chan ttyApproval, 1),
	}
	runner.SetWriteApprover(input.approveWrite)
	runner.SetCommandApprover(input.approveCommand)
	stopWorking := startWorkingStatus(output)
	defer stopWorking()
	statusOutput := workingOutput{output: output, stop: stopWorking}
	inputDone := make(chan error, 1)
	go func() {
		inputDone <- input.listen(runCtx, statusOutput)
	}()

	result, runErr := runTask(runCtx, runner, task, statusOutput)
	interrupt(context.Canceled)
	inputErr := <-inputDone
	restoreErr := term.Restore(fd, state)
	restored = true
	if runErr != nil {
		return result, runErr
	}
	if inputErr != nil && !errors.Is(inputErr, context.Canceled) {
		return agent.RunResult{}, fmt.Errorf("listen for Esc: %w", inputErr)
	}
	if restoreErr != nil {
		return agent.RunResult{}, fmt.Errorf("restore terminal: %w", restoreErr)
	}
	return result, nil
}

func (input *ttyRunInput) approveWrite(ctx context.Context, request tools.WriteRequest) (bool, error) {
	return input.approve(ctx, fmt.Sprintf("允许 %s 写入 %q？[Y/n] ", request.Tool, request.Path))
}

func (input *ttyRunInput) approveCommand(ctx context.Context, request tools.CommandRequest) (bool, error) {
	command, err := json.Marshal(append([]string{request.Command}, request.Args...))
	if err != nil {
		return false, fmt.Errorf("format command approval: %w", err)
	}
	return input.approve(ctx, fmt.Sprintf("允许 %s 执行 %s？[Y/n] ", request.Tool, command))
}

func (input *ttyRunInput) approve(ctx context.Context, prompt string) (bool, error) {
	approval := ttyApproval{prompt: prompt, result: make(chan bool, 1)}
	select {
	case input.approvals <- approval:
	case <-ctx.Done():
		return false, context.Cause(ctx)
	}
	select {
	case approved := <-approval.result:
		return approved, nil
	case <-ctx.Done():
		return false, context.Cause(ctx)
	}
}

func (input *ttyRunInput) listen(ctx context.Context, output io.Writer) error {
	var current *ttyApproval
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case approval := <-input.approvals:
			current = &approval
			fmt.Fprint(output, approval.prompt)
		default:
		}

		ready, err := pollTTYInput(input.fd)
		if err != nil {
			input.interrupt(err)
			return err
		}
		if !ready {
			continue
		}
		var buffer [1]byte
		read, err := unix.Read(input.fd, buffer[:])
		if err != nil {
			if errors.Is(err, unix.EINTR) || errors.Is(err, unix.EAGAIN) {
				continue
			}
			input.interrupt(err)
			return err
		}
		if read == 0 {
			continue
		}
		if buffer[0] == ttyEscape {
			input.interrupt(agent.ErrRunInterrupted)
			continue
		}
		if current == nil {
			continue
		}

		switch buffer[0] {
		case 'y', 'Y':
			fmt.Fprintln(output, string(buffer[0]))
			current.result <- true
			current = nil
		case 'n', 'N':
			fmt.Fprintln(output, string(buffer[0]))
			current.result <- false
			current = nil
		case ttyCarriageReturn, ttyLineFeed:
			fmt.Fprintln(output)
			current.result <- true
			current = nil
		}
	}
}

func pollTTYInput(fd int) (bool, error) {
	fds := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
	ready, err := unix.Poll(fds, ttyPollTimeoutMS)
	if err != nil {
		if errors.Is(err, unix.EINTR) {
			return false, nil
		}
		return false, err
	}
	return ready > 0 && fds[0].Revents&unix.POLLIN != 0, nil
}
