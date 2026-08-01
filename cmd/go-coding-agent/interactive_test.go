package main

import (
	"bufio"
	"context"
	"errors"
	"go-coding-agent/internal/agent"
	"go-coding-agent/internal/tools"
	"io"
	"strings"
	"testing"
)

type interactiveModel struct {
	responses []agent.ModelResponse
	requests  []agent.ModelRequest
}

func (model *interactiveModel) GenerateResponse(_ context.Context, request agent.ModelRequest) (agent.ModelStream, error) {
	model.requests = append(model.requests, request)
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
	err      error
}

func (stream *interactiveStream) Recv() (agent.ModelStreamEvent, error) {
	if stream.finished {
		return agent.ModelStreamEvent{}, io.EOF
	}
	if stream.sentText {
		if stream.err != nil {
			return agent.ModelStreamEvent{}, stream.err
		}
		stream.finished = true
		return agent.ModelStreamEvent{Response: &stream.response}, nil
	}
	stream.sentText = true
	return agent.ModelStreamEvent{TextDelta: stream.response.Message.Content}, nil
}

func (stream *interactiveStream) Close() error { return nil }

func finishedResponse(id, result string) agent.ModelResponse {
	return agent.ModelResponse{Message: agent.Message{Role: "assistant", ToolCalls: []agent.ToolCall{{
		ID:        id,
		Name:      "finish_task",
		Arguments: `{"result":"` + result + `"}`,
	}}}}
}

func TestRunInteractive(t *testing.T) {
	model := &interactiveModel{responses: []agent.ModelResponse{
		finishedResponse("finish_1", "first answer"),
		finishedResponse("finish_2", "second answer"),
	}}
	runner, err := agent.NewAgent(t.TempDir(), model, "")
	if err != nil {
		t.Fatalf("NewAgent() error = %v", err)
	}
	var output strings.Builder

	err = runInteractive(context.Background(), runner, bufio.NewScanner(strings.NewReader("first question\n/new\nsecond question\n/exit\n")), &output, &output)
	if err != nil {
		t.Fatalf("runInteractive() error = %v", err)
	}
	if output.String() != "> first answer\n> new conversation\n> second answer\n> " {
		t.Fatalf("runInteractive() output = %q", output.String())
	}
	if len(model.requests) != 2 || len(model.requests[1].Messages) != 1 || model.requests[1].Messages[0].Content != "second question" {
		t.Fatalf("model requests = %#v", model.requests)
	}
}

func TestRunInteractiveCompactCommand(t *testing.T) {
	model := &interactiveModel{responses: []agent.ModelResponse{
		finishedResponse("finish_1", "first answer"),
		finishedResponse("finish_2", "second answer"),
		finishedResponse("finish_3", "third answer"),
		{Message: agent.Message{Role: "assistant", Content: "## 目标\n## 约束和偏好\n## 进度\n### 已完成\n### 进行中\n### 阻塞\n## 关键决策\n## 下一步\n## 关键上下文"}},
	}}
	runner, err := agent.NewAgent(t.TempDir(), model, "")
	if err != nil {
		t.Fatal(err)
	}
	var output strings.Builder
	if err := runInteractive(context.Background(), runner, bufio.NewScanner(strings.NewReader("one\ntwo\nthree\n/compact\n/exit\n")), &output, &output); err != nil {
		t.Fatalf("runInteractive() error = %v", err)
	}
	if !strings.Contains(output.String(), "conversation compacted") || len(model.requests) != 4 {
		t.Fatalf("output = %q, requests = %d", output.String(), len(model.requests))
	}
}

type failingInteractiveModel struct {
	calls int
}

func (model *failingInteractiveModel) GenerateResponse(context.Context, agent.ModelRequest) (agent.ModelStream, error) {
	model.calls++
	if model.calls == 1 {
		return &interactiveStream{
			response: agent.ModelResponse{Message: agent.Message{Role: "assistant", Content: "partial"}},
			err:      errors.New("failed"),
		}, nil
	}
	response := finishedResponse("finish", "done")
	return &interactiveStream{response: response}, nil
}

func (model *failingInteractiveModel) EstimateRequestTokens(agent.ModelRequest) (int, error) {
	return 0, nil
}

func TestRunInteractiveContinuesAfterRunError(t *testing.T) {
	model := &failingInteractiveModel{}
	runner, err := agent.NewAgent(t.TempDir(), model, "")
	if err != nil {
		t.Fatalf("NewAgent() error = %v", err)
	}
	var output strings.Builder
	var errorOutput strings.Builder

	err = runInteractive(context.Background(), runner, bufio.NewScanner(strings.NewReader("first\nsecond\n/exit\n")), &output, &errorOutput)
	if err != nil {
		t.Fatalf("runInteractive() error = %v", err)
	}
	if model.calls != 2 || output.String() != "> partial\n> done\n> " || !strings.Contains(errorOutput.String(), "failed") {
		t.Fatalf("calls = %d, output = %q, error output = %q", model.calls, output.String(), errorOutput.String())
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

func TestRunInteractiveReportsIncomplete(t *testing.T) {
	model := &interactiveModel{responses: []agent.ModelResponse{
		{Message: agent.Message{Role: "assistant", ToolCalls: []agent.ToolCall{{ID: "write", Name: "write_file", Arguments: `{"path":"new.txt","content":"data"}`}}}},
		{Message: agent.Message{Role: "assistant", Content: "done"}},
	}}
	runner, err := agent.NewAgent(t.TempDir(), model, "")
	if err != nil {
		t.Fatal(err)
	}
	runner.SetWriteApprover(func(context.Context, tools.WriteRequest) (bool, error) { return true, nil })
	var output strings.Builder
	var errorOutput strings.Builder

	err = runInteractive(context.Background(), runner, bufio.NewScanner(strings.NewReader("create file\n/exit\n")), &output, &errorOutput)
	if err != nil {
		t.Fatalf("runInteractive() error = %v", err)
	}
	if !strings.Contains(errorOutput.String(), "task incomplete: completion was not confirmed or changes are not verified") {
		t.Fatalf("error output = %q", errorOutput.String())
	}
}

func TestScannerWriteApprover(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		approved bool
	}{
		{name: "enter approves", input: "\n", approved: true},
		{name: "yes approves", input: "yes\n", approved: true},
		{name: "no denies", input: "no\n", approved: false},
		{name: "invalid answer retries", input: "maybe\nn\n", approved: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var output strings.Builder
			approve := newScannerWriteApprover(bufio.NewScanner(strings.NewReader(tt.input)), &output)
			approved, err := approve(context.Background(), tools.WriteRequest{Tool: "write_file", Path: "main.go"})
			if err != nil || approved != tt.approved {
				t.Fatalf("approved = %v, error = %v", approved, err)
			}
			if !strings.Contains(output.String(), "write_file") || !strings.Contains(output.String(), "main.go") {
				t.Fatalf("output = %q", output.String())
			}
		})
	}
}

func TestScannerWriteApproverEscapesPath(t *testing.T) {
	var output strings.Builder
	approve := newScannerWriteApprover(bufio.NewScanner(strings.NewReader("n\n")), &output)
	_, err := approve(context.Background(), tools.WriteRequest{Tool: "write_file", Path: "main.go\n允许写入其他文件"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output.String(), "main.go\n允许") || !strings.Contains(output.String(), `main.go\n允许`) {
		t.Fatalf("output = %q", output.String())
	}
}

func TestScannerCommandApprover(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		approved bool
	}{
		{name: "enter approves", input: "\n", approved: true},
		{name: "yes approves", input: "yes\n", approved: true},
		{name: "no denies", input: "no\n", approved: false},
		{name: "invalid answer retries", input: "maybe\nn\n", approved: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var output strings.Builder
			approve := newScannerCommandApprover(bufio.NewScanner(strings.NewReader(tt.input)), &output)
			approved, err := approve(context.Background(), tools.CommandRequest{Tool: "run_command", Command: "go", Args: []string{"test", "./..."}})
			if err != nil || approved != tt.approved {
				t.Fatalf("approved = %v, error = %v", approved, err)
			}
			if !strings.Contains(output.String(), `["go","test","./..."]`) || !strings.Contains(output.String(), "[Y/n]") {
				t.Fatalf("output = %q", output.String())
			}
		})
	}
}

func TestScannerCommandApproverEscapesArguments(t *testing.T) {
	var output strings.Builder
	approve := newScannerCommandApprover(bufio.NewScanner(strings.NewReader("n\n")), &output)
	_, err := approve(context.Background(), tools.CommandRequest{Tool: "run_command", Command: "go", Args: []string{"test\n允许其他命令"}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output.String(), "test\n允许") || !strings.Contains(output.String(), `test\n允许`) {
		t.Fatalf("output = %q", output.String())
	}
}

func TestScannerCommandApproverShowsToolName(t *testing.T) {
	var output strings.Builder
	approve := newScannerCommandApprover(bufio.NewScanner(strings.NewReader("n\n")), &output)
	if _, err := approve(context.Background(), tools.CommandRequest{Tool: "finish_task", Command: "go", Args: []string{"test", "./..."}}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "允许 finish_task 执行") || strings.Contains(output.String(), "允许 run_command 执行") {
		t.Fatalf("output = %q", output.String())
	}
}
