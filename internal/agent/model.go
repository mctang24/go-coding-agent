package agent

import (
	"context"
	"errors"
	"go-coding-agent/internal/tools"
)

var ErrRateLimit = errors.New("model rate limit")

// Model is the provider-independent interface used by Agent.
type Model interface {
	GenerateResponse(context.Context, ModelRequest) (ModelStream, error)
	EstimateRequestTokens(ModelRequest) (int, error)
}

type TextDeltaHandler func(string) error

type ModelStreamEvent struct {
	TextDelta string
	Response  *ModelResponse
}

type ModelStream interface {
	Recv() (ModelStreamEvent, error)
	Close() error
}

type ModelRequest struct {
	Instructions string
	Messages     []Message
	Tools        []ToolDefinition
}

type ModelResponse struct {
	Message Message
	Usage   TokenUsage
}

type Message struct {
	Role        string       `json:"role"`
	Content     string       `json:"content,omitempty"`
	ToolCalls   []ToolCall   `json:"toolCalls,omitempty"`
	ToolResults []ToolResult `json:"toolResults,omitempty"`
	RawMessage  []byte       `json:"rawMessage,omitempty"`
}

type ToolCall struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type ToolResult struct {
	ToolCallID string `json:"toolCallId"`
	Content    string `json:"content"`
	IsError    bool   `json:"isError,omitempty"`
}

type ToolDefinition struct {
	Name        string
	Description string
	Parameters  tools.Schema
}

// modelTools returns the tool descriptions sent to the model.
func modelTools(available []tools.Tool) []ToolDefinition {
	definitions := make([]ToolDefinition, 0, len(available))
	for _, tool := range available {
		definitions = append(definitions, ToolDefinition{
			Name:        tool.Name,
			Description: tool.Description,
			Parameters:  tool.Parameters,
		})
	}
	return definitions
}
