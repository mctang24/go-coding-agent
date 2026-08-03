# Go Coding Agent

一个用 Go 从零实现的 Coding Agent，也是面向 Harness Engineering 的可运行实践：通过 Agent Runtime、受控工具和验证闭环稳定完成代码任务。

[![Go](https://img.shields.io/badge/Go-1.26-00ADD8?logo=go)](https://go.dev/)
[![DeepSeek](https://img.shields.io/badge/LLM-DeepSeek-4D6BFE)](https://www.deepseek.com/)
![Coding Agent](https://img.shields.io/badge/AI-Coding_Agent-8A2BE2)

[English](README_EN.md)

## 核心亮点

- **Harness Engineering 核心能力**：将 Agent Runtime、受控工具、安全边界、上下文管理、完成性验证和 trace 组合为最小可运行 Harness。
- **会话可恢复**：对话、工具执行结果、验证状态与压缩上下文增量持久化，退出后可通过 Session ID 继续任务。
- **完整 Coding 闭环**：内置代码检索、文件读写和命令执行工具，并通过端到端的「定位 → 修改 → 测试」任务验证。
- **安全文件修改**：提供读取前置、哈希冲突检测、原子替换、工作区边界和符号链接越界防护，修改文件或执行命令前均需人工确认。

## 快速开始

需要 Go 1.26、`rg` 和 DeepSeek API Key。

```bash
export DEEPSEEK_API_KEY="your-api-key"
go run ./cmd/go-coding-agent -root .
```

启动后可以直接提交代码任务：

```text
> 定位 NormalizeTag 的问题，修复后运行测试
> /new
> /exit
```

也可以运行一次性任务：

```bash
go run ./cmd/go-coding-agent -root . "分析项目入口并说明主要调用链"
```

需要记录执行轨迹时，指定 trace 文件：

```bash
go run ./cmd/go-coding-agent -root . -trace /tmp/go-coding-agent-trace.jsonl "分析项目入口"
```

文件修改和命令执行会在终端中逐次请求确认。会话仅保存在当前进程内，不跨进程持久化。
