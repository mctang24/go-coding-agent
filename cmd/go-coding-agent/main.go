package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"

	"go-coding-agent/internal/agent"
	"go-coding-agent/internal/deepseek"
	"go-coding-agent/internal/trace"
)

const systemPrompt = `你是终端代码检索专家，务必严格遵守以下规则：
1. 除代码、命令、路径和错误原文外，所有面向用户的自然语言必须使用中文。最终回答要像简洁的同事交接，默认不超过 10 行；必须使用终端纯文本，不要输出 Markdown 标记、标题、表格、代码围栏、反引号或粗体符号，列表只使用普通短横线；合并相关内容，只保留理解结论所需的信息。
2. 所有工具的 path 必须相对工作目录：根目录只能写 "."，禁止绝对路径。
3. 根据目标选择工具：查找文件名或了解目录结构时使用 list_files；查找符号、函数签名或代码内容时使用 search_text；同一路径的独立搜索必须放进一次调用的 patterns 数组。
4. 只提交已有依据的搜索；搜索无结果时只允许缩短关键词重试一次，仍无结果就停止该分支，禁止重复搜索。
5. 已定位文件后，批量读取已有依据且互不依赖的文件；同一文件只读取一次，禁止传入工具 Schema 未定义的参数。
6. 每轮工具返回后检查用户问题是否已有足够证据；足够则立即回答，只有能明确指出仍缺少哪条证据时才能继续调用工具。
7. 纯只读任务可以直接返回最终回答；涉及文件修改或命令执行时，任务完成必须单独调用 finish_task，不能与其他工具一起调用，并在其中提供 command 和 args 执行最终测试、构建、lint 或其他完成性检查。工作已经完成并准备执行最终检查时，必须直接调用 finish_task，禁止先用 run_command 执行同一检查；run_command 只用于任务尚未完成时的诊断和中间操作。
8. 只要本轮需要调用工具，响应中禁止输出任何自然语言、计划、状态、过程、过渡语或部分结论；不得先输出文字再调用工具，必须直接返回 tool_calls，content 必须严格为空。`

func main() {
	runStatus := runCLI()
	if code := exitCode(runStatus); code != 0 {
		os.Exit(code)
	}
}

func runCLI() agent.RunStatus {
	config, err := parseArgs(os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return agent.RunStatusError
	}
	fd, err := newCLIInput(os.Stdin)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return agent.RunStatusError
	}
	client, err := deepseek.NewClient(os.Getenv("DEEPSEEK_API_KEY"), "", "")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return agent.RunStatusError
	}
	runner, err := agent.NewSessionAgent(config.root, client, systemPrompt, config.sessionID)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return agent.RunStatusError
	}
	return runAgentCLI(config, runner, fd, os.Stdout, os.Stderr)
}

func runAgentCLI(config config, runner *agent.Agent, fd int, output, errorOutput io.Writer) agent.RunStatus {
	defer func() {
		printSessionID(output, runner.SessionID())
	}()
	ctx, cancel := context.WithCancelCause(context.Background())
	interrupts := make(chan os.Signal, 1)
	signal.Notify(interrupts, os.Interrupt)
	defer signal.Stop(interrupts)
	defer cancel(context.Canceled)
	go func() {
		select {
		case <-interrupts:
			cancel(agent.ErrRunInterrupted)
		case <-ctx.Done():
		}
	}()

	if config.tracePath != "" {
		if err := runner.EnableTrace(trace.Writer{Path: config.tracePath}); err != nil {
			fmt.Fprintln(errorOutput, err)
			return agent.RunStatusError
		}
	}

	if config.task == "" {
		if err := runInteractive(ctx, runner, fd, output); err != nil {
			if errors.Is(err, agent.ErrRunInterrupted) {
				return agent.RunStatusInterrupted
			}
			fmt.Fprintln(errorOutput, err)
			return agent.RunStatusError
		}
		return agent.RunStatusSuccess
	}
	runOutput := &newlineTrackingWriter{Writer: output}
	result, err := runTaskWithInterrupt(ctx, runner, config.task, fd, runOutput)
	if runOutput.needsNewline {
		fmt.Fprintln(output)
	}
	if err != nil {
		fmt.Fprintln(errorOutput, err)
		return agent.RunStatusError
	}
	if result.Status == agent.RunStatusIncomplete {
		fmt.Fprintln(errorOutput, "task incomplete: completion was not confirmed or changes are not verified")
	} else if result.Status == agent.RunStatusInterrupted {
		fmt.Fprintln(errorOutput, "interrupted")
	}
	return result.Status
}

func exitCode(status agent.RunStatus) int {
	switch status {
	case agent.RunStatusSuccess:
		return 0
	case agent.RunStatusIncomplete, agent.RunStatusInterrupted:
		return 1
	default:
		return 2
	}
}
