package agent

import (
	"encoding/json"
	"testing"
)

func TestTokenUsageJSON(t *testing.T) {
	var usage TokenUsage
	if err := json.Unmarshal([]byte(`{"prompt_tokens":12,"completion_tokens":3,"total_tokens":15}`), &usage); err != nil {
		t.Fatalf("decode token usage: %v", err)
	}
	if usage.PromptTokens != 12 || usage.CompletionTokens != 3 || usage.TotalTokens != 15 {
		t.Fatalf("token usage = %#v", usage)
	}
}
