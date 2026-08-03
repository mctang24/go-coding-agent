package agent

import (
	"github.com/goccy/go-json"
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

func TestShouldCompact(t *testing.T) {
	tests := []struct {
		name          string
		tokenUsage    int
		contextWindow int
		want          bool
	}{
		{name: "below threshold", tokenUsage: 89, contextWindow: 100, want: false},
		{name: "at threshold", tokenUsage: 90, contextWindow: 100, want: true},
		{name: "above threshold", tokenUsage: 91, contextWindow: 100, want: true},
		{name: "rounds fractional threshold up", tokenUsage: 90, contextWindow: 101, want: false},
		{name: "reaches rounded threshold", tokenUsage: 91, contextWindow: 101, want: true},
		{name: "invalid window", tokenUsage: 90, contextWindow: 0, want: false},
		{name: "invalid usage", tokenUsage: -1, contextWindow: 100, want: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := shouldCompact(test.tokenUsage, test.contextWindow); got != test.want {
				t.Fatalf("shouldCompact(%d, %d) = %t, want %t", test.tokenUsage, test.contextWindow, got, test.want)
			}
		})
	}
}
