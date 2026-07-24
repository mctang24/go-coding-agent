package deepseek

import (
	"bare-agent/internal/agent"
	"bare-agent/internal/tools"
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

type message struct {
	Role             string     `json:"role"`
	Content          *string    `json:"content"`
	ReasoningContent *string    `json:"reasoning_content,omitempty"`
	ToolCalls        []toolCall `json:"tool_calls,omitempty"`
	ToolCallID       string     `json:"tool_call_id,omitempty"`
}

type functionDefinition struct {
	Name        string       `json:"name"`
	Description string       `json:"description"`
	Parameters  tools.Schema `json:"parameters"`
}

type toolDefinition struct {
	Type     string             `json:"type"`
	Function functionDefinition `json:"function"`
}

type chatCompletionRequest struct {
	Messages []message
	Tools    []toolDefinition
}

type thinkingConfig struct {
	Type string `json:"type"`
}

type streamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

func (client *DeepSeekClient) createChatCompletion(_ context.Context, input chatCompletionRequest) (*chatCompletionStream, error) {
	if client.apiKey == "" {
		return nil, fmt.Errorf("DeepSeek API key is empty")
	}
	if len(input.Messages) == 0 {
		return nil, fmt.Errorf("DeepSeek chat completion has no messages")
	}

	requestBody, err := json.Marshal(struct {
		Model    string           `json:"model"`
		Messages []message        `json:"messages"`
		Tools    []toolDefinition `json:"tools,omitempty"`
		Thinking thinkingConfig   `json:"thinking"`
		Stream   bool             `json:"stream"`
		Options  streamOptions    `json:"stream_options"`
	}{
		Model:    client.model,
		Messages: input.Messages,
		Tools:    input.Tools,
		Thinking: thinkingConfig{Type: "enabled"},
		Stream:   true,
		Options:  streamOptions{IncludeUsage: true},
	})
	if err != nil {
		return nil, fmt.Errorf("DeepSeek encode chat completion request: %w", err)
	}

	endpoint := strings.TrimRight(client.baseURL, "/") + "/chat/completions"
	request, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(requestBody))
	if err != nil {
		return nil, fmt.Errorf("DeepSeek create chat completion request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+client.apiKey)
	request.Header.Set("Content-Type", "application/json")

	httpResponse, err := client.httpClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("DeepSeek send chat completion request: %w", err)
	}
	if httpResponse.StatusCode >= http.StatusOK && httpResponse.StatusCode < http.StatusMultipleChoices {
		return newChatCompletionStream(httpResponse.Body, (len(requestBody)+3)/4), nil
	}
	errorBody, err := io.ReadAll(httpResponse.Body)
	_ = httpResponse.Body.Close()
	if err != nil {
		return nil, fmt.Errorf("DeepSeek read chat completion error response: %w", err)
	}
	return nil, fmt.Errorf("DeepSeek chat completion returned %s: %s", httpResponse.Status, errorBody)
}

type functionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type toolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function functionCall `json:"function"`
}

type assistantMessage struct {
	Role             string     `json:"role"`
	Content          *string    `json:"content"`
	ReasoningContent *string    `json:"reasoning_content"`
	ToolCalls        []toolCall `json:"tool_calls"`
}

type chatCompletionChunk struct {
	Usage   *agent.TokenUsage `json:"usage"`
	Choices []struct {
		FinishReason *string `json:"finish_reason"`
		Delta        struct {
			Role             string  `json:"role"`
			Content          *string `json:"content"`
			ReasoningContent *string `json:"reasoning_content"`
			ToolCalls        []struct {
				Index    int    `json:"index"`
				ID       string `json:"id"`
				Type     string `json:"type"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"delta"`
	} `json:"choices"`
}

type modelResponse struct {
	Message      assistantMessage
	FinishReason string
	Usage        agent.TokenUsage
}

type chatCompletionStream struct {
	body          io.ReadCloser
	reader        *bufio.Reader
	response      modelResponse
	content       strings.Builder
	reasoning     strings.Builder
	contentSeen   bool
	reasoningSeen bool
	roleSeen      bool
	done          bool
	toolCalls     map[int]*toolCall
}

type chatCompletionEvent struct {
	TextDelta string
	Response  *modelResponse
}

func newChatCompletionStream(body io.ReadCloser, fallbackPromptTokens int) *chatCompletionStream {
	return &chatCompletionStream{
		body:      body,
		reader:    bufio.NewReader(body),
		response:  modelResponse{Usage: agent.TokenUsage{PromptTokens: fallbackPromptTokens, TotalTokens: fallbackPromptTokens}},
		toolCalls: map[int]*toolCall{},
	}
}

func (stream *chatCompletionStream) Recv() (chatCompletionEvent, error) {
	if stream.done {
		return chatCompletionEvent{}, io.EOF
	}
	for {
		line, readErr := stream.reader.ReadString('\n')
		if readErr != nil && readErr != io.EOF {
			return chatCompletionEvent{}, fmt.Errorf("DeepSeek read chat completion stream: %w", readErr)
		}

		var text strings.Builder
		if strings.HasPrefix(line, "data:") {
			data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if data == "[DONE]" {
				stream.done = true
				response, err := stream.finalResponse()
				if err != nil {
					return chatCompletionEvent{}, err
				}
				return chatCompletionEvent{Response: &response}, nil
			}

			var chunk chatCompletionChunk
			if err := json.Unmarshal([]byte(data), &chunk); err != nil {
				return chatCompletionEvent{}, fmt.Errorf("DeepSeek decode chat completion stream: %w", err)
			}
			if chunk.Usage != nil {
				stream.response.Usage = *chunk.Usage
			}
			for _, choice := range chunk.Choices {
				if role := choice.Delta.Role; role != "" && role != "assistant" {
					return chatCompletionEvent{}, fmt.Errorf("DeepSeek chat completion message role is %q, want assistant", role)
				}
				stream.roleSeen = stream.roleSeen || choice.Delta.Role == "assistant"
				if choice.Delta.Content != nil {
					stream.contentSeen = true
					stream.content.WriteString(*choice.Delta.Content)
					text.WriteString(*choice.Delta.Content)
				}
				if choice.Delta.ReasoningContent != nil {
					stream.reasoningSeen = true
					stream.reasoning.WriteString(*choice.Delta.ReasoningContent)
				}
				if choice.FinishReason != nil {
					stream.response.FinishReason = *choice.FinishReason
				}
				for _, delta := range choice.Delta.ToolCalls {
					call := stream.toolCalls[delta.Index]
					if call == nil {
						call = &toolCall{}
						stream.toolCalls[delta.Index] = call
					}
					call.ID += delta.ID
					call.Type += delta.Type
					call.Function.Name += delta.Function.Name
					call.Function.Arguments += delta.Function.Arguments
				}
			}
		}
		if text.Len() > 0 {
			return chatCompletionEvent{TextDelta: text.String()}, nil
		}
		if readErr == io.EOF {
			return chatCompletionEvent{}, fmt.Errorf("DeepSeek chat completion stream ended before [DONE]")
		}
	}
}

func (stream *chatCompletionStream) finalResponse() (modelResponse, error) {
	if !stream.done {
		return modelResponse{}, fmt.Errorf("DeepSeek chat completion stream is not complete")
	}
	if stream.response.FinishReason == "" {
		return modelResponse{}, fmt.Errorf("DeepSeek chat completion stream has no finish reason")
	}
	if !stream.roleSeen {
		return modelResponse{}, fmt.Errorf("DeepSeek chat completion stream has no message role")
	}
	stream.response.Message.Role = "assistant"
	if stream.contentSeen {
		value := stream.content.String()
		stream.response.Message.Content = &value
	}
	if stream.reasoningSeen {
		value := stream.reasoning.String()
		stream.response.Message.ReasoningContent = &value
	}
	stream.response.Message.ToolCalls = nil
	for index := 0; index < len(stream.toolCalls); index++ {
		call := stream.toolCalls[index]
		if call == nil {
			return modelResponse{}, fmt.Errorf("DeepSeek chat completion stream is missing tool call index %d", index)
		}
		stream.response.Message.ToolCalls = append(stream.response.Message.ToolCalls, *call)
	}
	return stream.response, nil
}

func (stream *chatCompletionStream) Close() error {
	return stream.body.Close()
}
