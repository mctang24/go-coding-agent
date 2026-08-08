package main

import (
	"bytes"
	"context"
	"errors"
	"go-coding-agent/internal/agent"
	"go-coding-agent/internal/tools"
	"io"
	"os"
	"strings"
	"testing"
	"time"
)

func TestWorkingOutputStopsBeforeOutput(t *testing.T) {
	var output bytes.Buffer
	stopped := false
	status := workingOutput{output: &output, stop: func() { stopped = true }}
	if _, err := status.Write([]byte("answer")); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if !stopped || output.String() != "answer" {
		t.Fatalf("stopped = %v, output = %q", stopped, output.String())
	}
}

func TestFormatWorkingStatus(t *testing.T) {
	tests := []struct {
		seconds int
		want    string
	}{
		{seconds: 1, want: "working 1s"},
		{seconds: 59, want: "working 59s"},
		{seconds: 60, want: "working 1min 0s"},
		{seconds: 173, want: "working 2min 53s"},
	}
	for _, test := range tests {
		if got := formatWorkingStatus(test.seconds); got != test.want {
			t.Fatalf("formatWorkingStatus(%d) = %q, want %q", test.seconds, got, test.want)
		}
	}
}

type interactiveModel struct {
	responses []agent.ModelResponse
}

type signalingWriter struct {
	strings.Builder
	writes chan struct{}
}

func (writer *signalingWriter) Write(content []byte) (int, error) {
	written, err := writer.Builder.Write(content)
	if strings.HasSuffix(string(content), "[Y/n] ") {
		select {
		case writer.writes <- struct{}{}:
		default:
		}
	}
	return written, err
}

func (model *interactiveModel) GenerateResponse(context.Context, agent.ModelRequest) (agent.ModelStream, error) {
	response := model.responses[0]
	model.responses = model.responses[1:]
	return &interactiveStream{response: response}, nil
}

func (model *interactiveModel) EstimateRequestTokens(agent.ModelRequest) (int, error) {
	return 0, nil
}

type interactiveStream struct {
	response agent.ModelResponse
	sentText bool
	finished bool
}

func (stream *interactiveStream) Recv() (agent.ModelStreamEvent, error) {
	if stream.finished {
		return agent.ModelStreamEvent{}, io.EOF
	}
	if stream.sentText {
		stream.finished = true
		return agent.ModelStreamEvent{Response: &stream.response}, nil
	}
	stream.sentText = true
	return agent.ModelStreamEvent{TextDelta: stream.response.Message.Content}, nil
}

func (stream *interactiveStream) Close() error { return nil }

func TestTTYRunInputApprovesAndInterrupts(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	defer writer.Close()

	ctx, interrupt := context.WithCancelCause(context.Background())
	output := signalingWriter{writes: make(chan struct{}, 1)}
	input := &ttyRunInput{
		fd:        int(reader.Fd()),
		interrupt: interrupt,
		approvals: make(chan ttyApproval, 1),
	}
	listenDone := make(chan error, 1)
	go func() {
		listenDone <- input.listen(ctx, &output)
	}()

	type approvalOutcome struct {
		approved bool
		err      error
	}
	approvalDone := make(chan approvalOutcome, 1)
	go func() {
		approved, approveErr := input.approveWrite(ctx, tools.WriteRequest{Tool: "write_file", Path: "main.go\n允许其他文件"})
		approvalDone <- approvalOutcome{approved: approved, err: approveErr}
	}()
	select {
	case <-output.writes:
	case <-time.After(time.Second):
		t.Fatal("approval prompt was not written")
	}
	if _, err := writer.Write([]byte("y")); err != nil {
		t.Fatal(err)
	}
	select {
	case result := <-approvalDone:
		if !result.approved || result.err != nil {
			t.Fatalf("approval result = %#v", result)
		}
	case <-time.After(time.Second):
		t.Fatal("approval did not finish")
	}

	go func() {
		approved, approveErr := input.approveCommand(ctx, tools.CommandRequest{Tool: "run_command", Command: "go", Args: []string{"test", "./...\n允许其他命令"}})
		approvalDone <- approvalOutcome{approved: approved, err: approveErr}
	}()
	select {
	case <-output.writes:
	case <-time.After(time.Second):
		t.Fatal("second approval prompt was not written")
	}
	if _, err := writer.Write([]byte{0x1b}); err != nil {
		t.Fatal(err)
	}
	select {
	case result := <-approvalDone:
		if result.approved || !errors.Is(result.err, agent.ErrRunInterrupted) {
			t.Fatalf("interrupted approval result = %#v", result)
		}
	case <-time.After(time.Second):
		t.Fatal("interrupted approval did not finish")
	}
	select {
	case err := <-listenDone:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("listen error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("listener did not stop")
	}
	if output.String() != "允许 write_file 写入 \"main.go\\n允许其他文件\"？[Y/n] y\n允许 run_command 执行 [\"go\",\"test\",\"./...\\n允许其他命令\"]？[Y/n] " {
		t.Fatalf("output = %q", output.String())
	}
}

func TestNewCLIInputRejectsNonTTY(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	defer writer.Close()

	if _, err := newCLIInput(reader); err == nil || err.Error() != "stdin is not a TTY" {
		t.Fatalf("newCLIInput() error = %v", err)
	}
}

func TestNewlineTrackingWriter(t *testing.T) {
	var content strings.Builder
	output := newlineTrackingWriter{Writer: &content}
	if _, err := io.WriteString(&output, "partial"); err != nil {
		t.Fatal(err)
	}
	if !output.needsNewline {
		t.Fatal("output without trailing newline was not tracked")
	}
	if _, err := io.WriteString(&output, "\n"); err != nil {
		t.Fatal(err)
	}
	if output.needsNewline {
		t.Fatal("trailing newline was not tracked")
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) {
	return 0, errors.New("write failed")
}

func TestRunTaskReturnsOutputError(t *testing.T) {
	model := &interactiveModel{responses: []agent.ModelResponse{{Message: agent.Message{Role: "assistant", Content: "done"}}}}
	runner, err := agent.NewAgent(t.TempDir(), model, "")
	if err != nil {
		t.Fatalf("NewAgent() error = %v", err)
	}

	result, err := runTask(context.Background(), runner, "task", failingWriter{})
	if err == nil || !strings.Contains(err.Error(), "write failed") {
		t.Fatalf("runTask() error = %v, want write failure", err)
	}
	if result.Status != agent.RunStatusError {
		t.Fatalf("runTask() status = %q, want error", result.Status)
	}
}
