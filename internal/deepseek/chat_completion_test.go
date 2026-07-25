package deepseek

import (
	"bare-agent/internal/tools"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCreateChatCompletion(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %q, want POST", r.Method)
		}
		if r.URL.Path != "/chat/completions" {
			t.Errorf("path = %q, want /chat/completions", r.URL.Path)
		}
		if authorization := r.Header.Get("Authorization"); authorization != "Bearer test-key" {
			t.Errorf("Authorization = %q, want Bearer test-key", authorization)
		}
		if contentType := r.Header.Get("Content-Type"); contentType != "application/json" {
			t.Errorf("Content-Type = %q, want application/json", contentType)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read request body: %v", err)
		}
		for _, expected := range []string{`"model":"deepseek-v4-flash"`, `"role":"user"`, `"content":"find target"`, `"name":"search_text"`, `"thinking":{"type":"enabled"}`, `"stream":true`, `"stream_options":{"include_usage":true}`} {
			if !strings.Contains(string(body), expected) {
				t.Errorf("body = %q, want to contain %q", body, expected)
			}
		}
		_, _ = w.Write([]byte("data: {\"choices\":[{\"finish_reason\":null,\"delta\":{\"role\":\"assistant\",\"content\":\"do\"}}]}\n\n" +
			"data: {\"choices\":[{\"finish_reason\":\"stop\",\"delta\":{\"content\":\"ne\"}}]}\n\n" +
			"data: {\"choices\":[],\"usage\":{\"prompt_tokens\":17,\"completion_tokens\":9,\"total_tokens\":26}}\n\n" +
			"data: [DONE]\n\n"))
	}))
	defer server.Close()

	client := DeepSeekClient{
		httpClient: server.Client(),
		baseURL:    server.URL,
		apiKey:     "test-key",
		model:      "deepseek-v4-flash",
	}
	stream, err := client.createChatCompletion(context.Background(), chatCompletionRequest{
		Messages: []message{{Role: "user", Content: new("find target")}},
		Tools: []toolDefinition{{
			Type: "function",
			Function: functionDefinition{
				Name:        "search_text",
				Description: "search text",
				Parameters:  tools.ObjectSchema(nil),
			},
		}},
	})
	if err != nil {
		t.Fatalf("createChatCompletion() error = %v", err)
	}
	streamed, response, err := readChatCompletionStream(stream)
	if err != nil {
		t.Fatalf("read chat completion stream: %v", err)
	}
	if response.Message.Content == nil || *response.Message.Content != "done" {
		t.Errorf("response content = %v, want done", response.Message.Content)
	}
	if streamed != "done" {
		t.Errorf("streamed content = %q, want done", streamed)
	}
	if response.Usage.PromptTokens != 17 || response.Usage.CompletionTokens != 9 || response.Usage.TotalTokens != 26 {
		t.Errorf("response usage = %#v, want 17 prompt, 9 completion, 26 total", response.Usage)
	}
}

func readChatCompletionStream(stream *chatCompletionStream) (string, modelResponse, error) {
	defer stream.Close()
	var text strings.Builder
	for {
		event, err := stream.Recv()
		if err != nil {
			return text.String(), modelResponse{}, err
		}
		if event.Response != nil {
			return text.String(), *event.Response, nil
		}
		text.WriteString(event.TextDelta)
	}
}

func TestCreateChatCompletionErrors(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		http.Error(w, "busy", http.StatusTooManyRequests)
	}))
	defer server.Close()

	tests := []struct {
		name     string
		ctx      context.Context
		apiKey   string
		messages []message
		wantErr  string
	}{
		{name: "empty API key", ctx: context.Background(), messages: []message{{Role: "user", Content: new("test")}}, wantErr: "API key is empty"},
		{name: "empty messages", ctx: context.Background(), apiKey: "test-key", wantErr: "has no messages"},
		{name: "non-success status", ctx: context.Background(), apiKey: "test-key", messages: []message{{Role: "user", Content: new("test")}}, wantErr: "429 Too Many Requests"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := DeepSeekClient{
				httpClient: server.Client(),
				baseURL:    server.URL,
				apiKey:     tt.apiKey,
				model:      "deepseek-v4-flash",
			}
			_, err := client.createChatCompletion(tt.ctx, chatCompletionRequest{Messages: tt.messages})
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("createChatCompletion() error = %v, want to contain %q", err, tt.wantErr)
			}
		})
	}
	if calls != 1 {
		t.Fatalf("server calls = %d, want 1", calls)
	}
}

func TestCreateChatCompletionDoesNotRetryServerError(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		http.Error(w, "busy", http.StatusInternalServerError)
	}))
	defer server.Close()

	client := DeepSeekClient{httpClient: server.Client(), baseURL: server.URL, apiKey: "test-key", model: "deepseek-v4-flash"}
	_, err := client.createChatCompletion(context.Background(), chatCompletionRequest{Messages: []message{{Role: "user", Content: new("test")}}})
	if err == nil || !strings.Contains(err.Error(), "500 Internal Server Error") {
		t.Fatalf("createChatCompletion() error = %v, want 500 Internal Server Error", err)
	}
	if calls != 1 {
		t.Fatalf("server calls = %d, want 1", calls)
	}
}

func TestCreateChatCompletionDoesNotRetryBadRequest(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		http.Error(w, "invalid request", http.StatusBadRequest)
	}))
	defer server.Close()

	client := DeepSeekClient{httpClient: server.Client(), baseURL: server.URL, apiKey: "test-key", model: "deepseek-v4-flash"}
	_, err := client.createChatCompletion(context.Background(), chatCompletionRequest{Messages: []message{{Role: "user", Content: new("test")}}})
	if err == nil || !strings.Contains(err.Error(), "400 Bad Request") {
		t.Fatalf("createChatCompletion() error = %v, want 400 Bad Request", err)
	}
	if calls != 1 {
		t.Fatalf("server calls = %d, want 1", calls)
	}
}

func TestParseChatCompletionStream(t *testing.T) {
	tests := []struct {
		name       string
		data       string
		wantFinish string
		wantText   string
		wantTool   string
		wantErr    bool
	}{
		{
			name:       "text response",
			data:       "data: {\"choices\":[{\"finish_reason\":null,\"delta\":{\"role\":\"assistant\",\"content\":\"do\"}}]}\n\ndata: {\"choices\":[{\"finish_reason\":\"stop\",\"delta\":{\"role\":\"assistant\",\"content\":\"ne\"}}]}\n\ndata: [DONE]\n\n",
			wantFinish: "stop",
			wantText:   "done",
		},
		{
			name:       "tool call response",
			data:       "data: {\"choices\":[{\"finish_reason\":null,\"delta\":{\"role\":\"assistant\",\"reasoning_content\":\"search first\",\"tool_calls\":[{\"index\":0,\"id\":\"call_1\",\"type\":\"function\",\"function\":{\"name\":\"search_text\",\"arguments\":\"{\\\"query\\\":\"}}]}}]}\n\ndata: {\"choices\":[{\"finish_reason\":\"tool_calls\",\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"\\\"target\\\"}\"}}]}}]}\n\ndata: [DONE]\n\n",
			wantFinish: "tool_calls",
			wantTool:   "search_text",
		},
		{
			name:    "invalid JSON",
			data:    "data: {\n\n",
			wantErr: true,
		},
		{
			name:    "missing done",
			data:    "data: {\"choices\":[]}\n\n",
			wantErr: true,
		},
		{
			name:    "missing finish reason",
			data:    "data: {\"choices\":[]}\n\ndata: [DONE]\n\n",
			wantErr: true,
		},
		{
			name:    "invalid message role",
			data:    "data: {\"choices\":[{\"finish_reason\":\"stop\",\"delta\":{\"role\":\"user\",\"content\":\"done\"}}]}\n\ndata: [DONE]\n\n",
			wantErr: true,
		},
		{
			name:    "missing message role",
			data:    "data: {\"choices\":[{\"finish_reason\":\"stop\",\"delta\":{\"content\":\"done\"}}]}\n\ndata: [DONE]\n\n",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stream := newChatCompletionStream(io.NopCloser(strings.NewReader(tt.data)))
			_, response, err := readChatCompletionStream(stream)
			if tt.wantErr {
				if err == nil {
					t.Fatal("parseChatCompletionStream() error = nil, want error")
				}
				return
			}
			if err != nil {
				t.Fatalf("parseChatCompletionStream() error = %v", err)
			}
			if response.FinishReason != tt.wantFinish {
				t.Errorf("FinishReason = %q, want %q", response.FinishReason, tt.wantFinish)
			}
			if tt.wantText != "" && (response.Message.Content == nil || *response.Message.Content != tt.wantText) {
				t.Errorf("Content = %v, want %q", response.Message.Content, tt.wantText)
			}
			if tt.wantTool != "" && (len(response.Message.ToolCalls) != 1 || response.Message.ToolCalls[0].Function.Name != tt.wantTool) {
				t.Errorf("ToolCalls = %#v, want tool %q", response.Message.ToolCalls, tt.wantTool)
			}
		})
	}
}

func TestParseChatCompletionStreamAcceptsLargeEvent(t *testing.T) {
	content := strings.Repeat("a", 1024*1024)
	data := "data: {\"choices\":[{\"finish_reason\":\"stop\",\"delta\":{\"role\":\"assistant\",\"content\":\"" + content + "\"}}]}\n\ndata: [DONE]\n\n"

	stream := newChatCompletionStream(io.NopCloser(strings.NewReader(data)))
	_, response, err := readChatCompletionStream(stream)
	if err != nil {
		t.Fatalf("parseChatCompletionStream() error = %v", err)
	}
	if response.Message.Content == nil {
		t.Fatal("content = nil")
	}
	if *response.Message.Content != content {
		t.Fatalf("content length = %d, want %d", len(*response.Message.Content), len(content))
	}
}

func TestParseChatCompletionStreamDoesNotEmitReasoning(t *testing.T) {
	data := "data: {\"choices\":[{\"finish_reason\":null,\"delta\":{\"role\":\"assistant\",\"reasoning_content\":\"hidden\"}}]}\n\ndata: {\"choices\":[{\"finish_reason\":\"stop\",\"delta\":{\"content\":\"shown\"}}]}\n\ndata: [DONE]\n\n"
	stream := newChatCompletionStream(io.NopCloser(strings.NewReader(data)))
	streamed, response, err := readChatCompletionStream(stream)
	if err != nil {
		t.Fatalf("parseChatCompletionStream() error = %v", err)
	}
	if streamed != "shown" {
		t.Fatalf("streamed content = %q, want shown", streamed)
	}
	if response.Message.ReasoningContent == nil || *response.Message.ReasoningContent != "hidden" {
		t.Fatalf("reasoning content = %v, want hidden", response.Message.ReasoningContent)
	}
}
