package agent

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"go-coding-agent/internal/tools"
	"os"
	"time"

	"go-coding-agent/internal/trace"
)

type runTrace struct {
	writer    *trace.Writer
	sessionID string
	runID     string
	startedAt time.Time
}

type traceToolDefinition struct {
	Name        string       `json:"name"`
	Description string       `json:"description"`
	Parameters  tools.Schema `json:"parameters"`
}

func reportTraceError(event string, err error) {
	if err != nil {
		fmt.Fprintf(os.Stderr, "trace %s error: %v\n", event, err)
	}
}

func newTraceID(prefix string) (string, error) {
	if prefix == "" {
		return "", fmt.Errorf("generate trace ID: prefix is empty")
	}
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("generate trace ID: %w", err)
	}
	return prefix + "_" + hex.EncodeToString(bytes), nil
}

func (agent *Agent) startRunTrace(task string, definitions []ToolDefinition) (*runTrace, error) {
	if agent.traceWriter == nil {
		return nil, nil
	}
	runID, err := newTraceID("run")
	if err != nil {
		return nil, fmt.Errorf("agent run: %w", err)
	}
	startedAt := time.Now()
	current := &runTrace{writer: agent.traceWriter, sessionID: agent.sessionID, runID: runID, startedAt: startedAt}
	traceTools := make([]traceToolDefinition, 0, len(definitions))
	for _, definition := range definitions {
		traceTools = append(traceTools, traceToolDefinition{
			Name:        definition.Name,
			Description: definition.Description,
			Parameters:  definition.Parameters,
		})
	}
	if err := current.append(trace.Event{
		Timestamp: startedAt.UTC(),
		Type:      "run_start",
		Data: map[string]any{
			"task":         task,
			"instructions": agent.instructions,
			"tools":        traceTools,
		},
	}); err != nil {
		return nil, fmt.Errorf("agent run: %w", err)
	}
	return current, nil
}

func (current *runTrace) append(event trace.Event) error {
	if current == nil {
		return nil
	}
	event.SessionID = current.sessionID
	event.RunID = current.runID
	return current.writer.Append(event)
}

func (current *runTrace) finish(runErr error, hasUnverifiedChange bool, verification *verificationFact) error {
	if current == nil {
		return nil
	}
	data := map[string]any{"status": "success"}
	if runErr != nil {
		data["status"] = "error"
		data["error"] = runErr.Error()
	} else if hasUnverifiedChange {
		data["status"] = "incomplete"
	}
	if verification != nil {
		data["verification"] = verification
	}
	return current.append(trace.Event{
		Timestamp:  time.Now().UTC(),
		Type:       "run_end",
		DurationMS: time.Since(current.startedAt).Milliseconds(),
		Data:       data,
	})
}
