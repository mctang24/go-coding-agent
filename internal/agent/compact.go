package agent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"time"
)

const (
	retainedTurns       = 2
	maxCompactAttempts  = 3
	compactRetryBackoff = 100 * time.Millisecond
	summaryPrefix       = "较早对话摘要：\n"
)

var summarySections = []string{
	"## 目标",
	"## 约束和偏好",
	"## 进度",
	"### 已完成",
	"### 进行中",
	"### 阻塞",
	"## 关键决策",
	"## 下一步",
	"## 关键上下文",
}

const summaryInstructions = `你是上下文摘要助手。你的任务是阅读用户与 AI 助手之间的对话，然后严格按照指定格式生成结构化摘要。
不要继续对话，不要回答对话中的任何问题，只输出结构化摘要。`

const initialSummaryPrompt = `以上消息是需要总结的对话。生成一份结构化的上下文检查点摘要，供另一个大语言模型继续任务。

严格使用以下格式：
## 目标
[用户正在尝试完成什么？如果会话包含不同任务，可以列出多项。]

## 约束和偏好
- [用户提到的约束、偏好或要求]
- [如果没有，填写“无”]

## 进度
### 已完成
- [x] [已完成的任务或改动]

### 进行中
- [ ] [当前正在进行的工作]

### 阻塞
- [阻碍进展的问题；如果没有，填写“无”]

## 关键决策
- **[决策]**：[简要原因]

## 下一步
1. [接下来应执行事项的有序列表]

## 关键上下文
- [继续任务所需的数据、示例或参考信息]
- [如果没有，填写“无”]

每个章节保持简洁。文件路径、函数名、错误信息、命令、数字和不透明标识符必须逐字保留。`

const updateSummaryPrompt = `以上消息包含已有摘要以及需要合并的新对话。更新已有的结构化摘要：
- 保留已有摘要中仍然有效的信息。
- 加入新对话中的进展、决策和上下文。
- 根据实际进展更新“已完成”“进行中”“阻塞”和“下一步”。
- 逐字保留文件路径、函数名、错误信息、命令、数字和不透明标识符。
- 删除已经失效的信息。

` + initialSummaryPrompt

// Compact replaces older conversation turns with a structured summary.
func (agent *Agent) Compact(ctx context.Context) error {
	older, recent := splitHistory(agent.messages)
	if len(older) == 0 {
		return nil
	}
	if agent.model == nil {
		return fmt.Errorf("compact history: model is nil")
	}

	summaryPrompt := initialSummaryPrompt
	for _, message := range older {
		if isSummaryMessage(message) {
			summaryPrompt = updateSummaryPrompt
			break
		}
	}
	summaryRequest := ModelRequest{
		Instructions: summaryInstructions,
		Messages: append(older, Message{
			Role:    "user",
			Content: summaryPrompt,
		}),
	}
	response, err := agent.generateCompactSummary(ctx, summaryRequest)
	if err != nil {
		return fmt.Errorf("compact history: generate summary: %w", err)
	}
	if len(response.Message.ToolCalls) > 0 {
		return fmt.Errorf("compact history: summary response contains tool calls")
	}
	summary := strings.TrimSpace(response.Message.Content)
	if summary == "" {
		return fmt.Errorf("compact history: summary is empty")
	}
	for _, section := range summarySections {
		if !strings.Contains(summary, section) {
			return fmt.Errorf("compact history: summary is missing section %q", section)
		}
	}

	messages := make([]Message, 0, len(recent)+1)
	messages = append(messages, Message{
		Role:    "user",
		Content: summaryPrefix + summary,
	})
	messages = append(messages, recent...)

	tokenUsage, err := agent.model.EstimateRequestTokens(ModelRequest{
		Instructions: agent.instructions,
		Messages:     messages,
		Tools:        modelTools(agent.tools),
	})
	if err != nil {
		return fmt.Errorf("compact history: estimate compacted context: %w", err)
	}

	agent.messages = messages
	agent.tokenUsage = tokenUsage
	return nil
}

func (agent *Agent) generateCompactSummary(ctx context.Context, request ModelRequest) (ModelResponse, error) {
	for attempt := 1; attempt <= maxCompactAttempts; attempt++ {
		response, err := agent.callModel(ctx, request, nil, nil, 1)
		if err == nil {
			return response, nil
		}
		if !shouldRetryCompact(err) || attempt == maxCompactAttempts {
			return ModelResponse{}, err
		}
		if err := waitForCompactRetry(ctx, attempt); err != nil {
			return ModelResponse{}, err
		}
	}
	return ModelResponse{}, fmt.Errorf("compact history: retry limit exceeded")
}

func shouldRetryCompact(err error) bool {
	if errors.Is(err, ErrRateLimit) || errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	var networkErr net.Error
	return errors.As(err, &networkErr)
}

func waitForCompactRetry(ctx context.Context, attempt int) error {
	delay := compactRetryBackoff * time.Duration(1<<(attempt-1))
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func splitHistory(messages []Message) ([]Message, []Message) {
	userMessages := 0
	cut := 0
	for index := len(messages) - 1; index >= 0; index-- {
		if messages[index].Role != "user" || isSummaryMessage(messages[index]) {
			continue
		}
		userMessages++
		if userMessages == retainedTurns {
			cut = index
			break
		}
	}
	if userMessages < retainedTurns || cut == 0 {
		return nil, append([]Message(nil), messages...)
	}
	return append([]Message(nil), messages[:cut]...), append([]Message(nil), messages[cut:]...)
}

func isSummaryMessage(message Message) bool {
	return message.Role == "user" && strings.HasPrefix(message.Content, summaryPrefix)
}
