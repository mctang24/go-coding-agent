package agent

const compactThresholdPercent = 90

type TokenUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

func shouldCompact(tokenUsage, contextWindow int) bool {
	if tokenUsage < 0 || contextWindow <= 0 {
		return false
	}
	return tokenUsage*100 >= contextWindow*compactThresholdPercent
}
