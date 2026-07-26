package deepseek

import (
	"bare-agent/internal/agent"
	"context"
	"encoding/json"
	"fmt"
)

// GenerateResponse generates a response with DeepSeek.
func (client *DeepSeekClient) GenerateResponse(ctx context.Context, request agent.ModelRequest) (agent.ModelStream, error) {
	input, err := convertRequest(request)
	if err != nil {
		return nil, err
	}

	stream, err := client.createChatCompletion(ctx, input)
	if err != nil {
		return nil, err
	}
	return &modelStream{stream: stream}, nil
}

// EstimateRequestTokens estimates the serialized DeepSeek request size in tokens.
func (client *DeepSeekClient) EstimateRequestTokens(request agent.ModelRequest) (int, error) {
	input, err := convertRequest(request)
	if err != nil {
		return 0, err
	}
	requestBody, err := client.encodeChatCompletionRequest(input)
	if err != nil {
		return 0, err
	}
	return (len(requestBody) + 3) / 4, nil
}

func convertRequest(request agent.ModelRequest) (chatCompletionRequest, error) {
	messages, err := convertMessages(request.Instructions, request.Messages)
	if err != nil {
		return chatCompletionRequest{}, err
	}
	definitions := make([]toolDefinition, 0, len(request.Tools))
	for _, tool := range request.Tools {
		definitions = append(definitions, toolDefinition{
			Type: "function",
			Function: functionDefinition{
				Name:        tool.Name,
				Description: tool.Description,
				Parameters:  tool.Parameters,
			},
		})
	}
	return chatCompletionRequest{Messages: messages, Tools: definitions}, nil
}

func convertMessages(instructions string, inputs []agent.Message) ([]message, error) {
	messages := make([]message, 0, len(inputs)+1)
	if instructions != "" {
		messages = append(messages, message{Role: "system", Content: &instructions})
	}

	for index, input := range inputs {
		// RawMessage 存在时以其为准，忽略只读的 Content 和 ToolCalls；手动构造 assistant 消息时不设置 RawMessage。
		if len(input.RawMessage) > 0 {
			if input.Role != "" && input.Role != "assistant" {
				return nil, fmt.Errorf("DeepSeek raw message %d has conflicting role %q", index, input.Role)
			}
			if len(input.ToolResults) > 0 {
				return nil, fmt.Errorf("DeepSeek assistant message %d contains tool results", index)
			}
			var raw message
			if err := json.Unmarshal(input.RawMessage, &raw); err != nil {
				return nil, fmt.Errorf("DeepSeek decode raw message %d: %w", index, err)
			}
			if raw.Role != "assistant" {
				return nil, fmt.Errorf("DeepSeek raw message %d role is %q, want assistant", index, raw.Role)
			}
			messages = append(messages, raw)
			continue
		}

		// user 传内容，assistant 传回复或调用，tool 传执行结果。
		switch input.Role {
		case "user":
			if len(input.ToolCalls) > 0 || len(input.ToolResults) > 0 {
				return nil, fmt.Errorf("DeepSeek user message %d contains tool data", index)
			}
			messages = append(messages, message{Role: "user", Content: &input.Content})
		case "assistant":
			if len(input.ToolResults) > 0 {
				return nil, fmt.Errorf("DeepSeek assistant message %d contains tool results", index)
			}
			converted := message{Role: "assistant", Content: &input.Content}
			for _, call := range input.ToolCalls {
				converted.ToolCalls = append(converted.ToolCalls, toolCall{
					ID:   call.ID,
					Type: "function",
					Function: functionCall{
						Name:      call.Name,
						Arguments: call.Arguments,
					},
				})
			}
			messages = append(messages, converted)
		case "tool":
			if len(input.ToolResults) == 0 {
				return nil, fmt.Errorf("DeepSeek tool message %d has no results", index)
			}
			if input.Content != "" || len(input.ToolCalls) > 0 {
				return nil, fmt.Errorf("DeepSeek tool message %d contains non-result data", index)
			}
			for _, result := range input.ToolResults {
				content := result.Content
				messages = append(messages, message{Role: "tool", Content: &content, ToolCallID: result.ToolCallID})
			}
		default:
			return nil, fmt.Errorf("DeepSeek message %d has unsupported role %q", index, input.Role)
		}
	}
	return messages, nil
}

type modelStream struct {
	stream *chatCompletionStream
}

func (stream *modelStream) Recv() (agent.ModelStreamEvent, error) {
	event, err := stream.stream.Recv()
	if err != nil {
		return agent.ModelStreamEvent{}, err
	}
	if event.Response == nil {
		return agent.ModelStreamEvent{TextDelta: event.TextDelta}, nil
	}
	response := *event.Response
	if response.FinishReason != "stop" && response.FinishReason != "tool_calls" {
		return agent.ModelStreamEvent{}, fmt.Errorf("DeepSeek chat completion stopped with finish reason %q", response.FinishReason)
	}
	if response.FinishReason == "tool_calls" && len(response.Message.ToolCalls) == 0 {
		return agent.ModelStreamEvent{}, fmt.Errorf("DeepSeek chat completion stopped for tool calls but returned none")
	}
	if response.FinishReason == "stop" && len(response.Message.ToolCalls) > 0 {
		return agent.ModelStreamEvent{}, fmt.Errorf("DeepSeek chat completion stopped normally but returned tool calls")
	}

	rawMessage, err := json.Marshal(response.Message)
	if err != nil {
		return agent.ModelStreamEvent{}, fmt.Errorf("DeepSeek encode raw assistant message: %w", err)
	}
	output := agent.Message{Role: "assistant", RawMessage: rawMessage}
	if response.Message.Content != nil {
		output.Content = *response.Message.Content
	}
	for _, call := range response.Message.ToolCalls {
		output.ToolCalls = append(output.ToolCalls, agent.ToolCall{
			ID:        call.ID,
			Name:      call.Function.Name,
			Arguments: call.Function.Arguments,
		})
	}

	finalResponse := agent.ModelResponse{
		Message: output,
		Usage:   response.Usage,
	}
	return agent.ModelStreamEvent{Response: &finalResponse}, nil
}

func (stream *modelStream) Close() error {
	return stream.stream.Close()
}
