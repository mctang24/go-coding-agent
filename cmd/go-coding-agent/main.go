package main

import (
	"bufio"
	"context"
	"fmt"
	"go-coding-agent/internal/agent"
	"go-coding-agent/internal/deepseek"
	"go-coding-agent/internal/trace"
	"os"
)

const systemPrompt = `你是终端代码检索专家，务必严格遵守以下规则：
1. 最终回答要像简洁的同事交接，默认不超过 10 行；简单回答不要使用标题或重型格式，合并相关内容，只保留理解结论所需的信息。
2. 所有工具的 path 必须相对工作目录：根目录只能写 "."，禁止绝对路径。
3. 根据目标选择工具：查找文件名或了解目录结构时使用 list_files；查找符号、函数签名或代码内容时使用 search_text；同一路径的独立搜索必须放进一次调用的 patterns 数组。
4. 只提交已有依据的搜索；搜索无结果时只允许缩短关键词重试一次，仍无结果就停止该分支，禁止重复搜索。
5. 已定位文件后，批量读取已有依据且互不依赖的文件；同一文件只读取一次，禁止传入工具 Schema 未定义的参数。
6. 每轮工具返回后检查用户问题是否已有足够证据；足够则立即回答，只有能明确指出仍缺少哪条证据时才能继续调用工具。
7. 任务完成时必须单独调用 finish_task，不能与其他工具一起调用；纯读取任务只提供 result。成功执行 edit_file 或 write_file，或 run_command 实际启动后，必须在 finish_task 中提供 command 和 args 执行最终测试、构建、lint 或其他完成性检查。工作已经完成并准备执行最终检查时，必须直接调用 finish_task，禁止先用 run_command 执行同一检查；run_command 只用于任务尚未完成时的诊断和中间操作。
8. 只要本轮调用工具，就只能返回 tool_calls，content 必须严格为空；禁止计划、状态、过程、过渡语和部分结论。`

func main() {
	config, err := parseArgs(os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	client, err := deepseek.NewClient(os.Getenv("DEEPSEEK_API_KEY"), "", "")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	runner, err := agent.NewAgent(config.root, client, systemPrompt)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if config.tracePath != "" {
		if err := runner.EnableTrace(trace.Writer{Path: config.tracePath}); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}

	ctx := context.Background()
	scanner := bufio.NewScanner(os.Stdin)
	runner.SetWriteApprover(newScannerWriteApprover(scanner, os.Stdout))
	runner.SetCommandApprover(newScannerCommandApprover(scanner, os.Stdout))

	if config.task == "" {
		if err := runInteractive(ctx, runner, scanner, os.Stdout, os.Stderr); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	result, err := runTask(ctx, runner, config.task, os.Stdout)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(exitCode(result.Status))
	}
	fmt.Println()
	if result.Status == agent.RunStatusIncomplete {
		fmt.Fprintln(os.Stderr, "task incomplete: completion was not confirmed or changes are not verified")
		os.Exit(exitCode(result.Status))
	}
}

func exitCode(status agent.RunStatus) int {
	switch status {
	case agent.RunStatusSuccess:
		return 0
	case agent.RunStatusIncomplete:
		return 1
	default:
		return 2
	}
}
