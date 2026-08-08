package agent

import (
	"context"
	"fmt"
)

// executeTool finds and executes a tool requested by the model.
func (agent *Agent) executeTool(ctx context.Context, name, arguments string) (string, error) {
	if name == "" {
		return "", fmt.Errorf("agent execute tool: name is empty")
	}
	for _, tool := range agent.tools {
		if tool.Name != name {
			continue
		}
		if tool.Execute == nil {
			return "", fmt.Errorf("agent execute tool %q: execute function is nil", name)
		}

		result, err := tool.Execute(ctx, agent.root, arguments)
		if err != nil {
			return "", fmt.Errorf("agent execute tool %q: %w", name, err)
		}
		return result, nil
	}

	return "", fmt.Errorf("agent execute tool %q: not registered", name)
}

// executeToolCall executes one model tool call and builds its result.
func (agent *Agent) executeToolCall(ctx context.Context, call ToolCall) (ToolResult, error) {
	if call.ID == "" {
		return ToolResult{}, fmt.Errorf("agent execute tool call: ID is empty")
	}

	result, err := agent.executeTool(ctx, call.Name, call.Arguments)
	if err != nil {
		if isRunInterrupted(ctx) {
			return ToolResult{ToolCallID: call.ID, Content: "aborted by user", IsError: true}, nil
		}
		return ToolResult{ToolCallID: call.ID, Content: err.Error(), IsError: true}, nil
	}
	return ToolResult{ToolCallID: call.ID, Content: result}, nil
}
