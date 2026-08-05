# Go Coding Agent

一个用 Go 从零构建、面向真实软件工程任务的 Coding Agent Harness，聚焦可靠执行、上下文管理与安全边界。

[![Go](https://img.shields.io/badge/Go-1.26-00ADD8?logo=go)](https://go.dev/)
![Coding Agent](https://img.shields.io/badge/AI-Coding_Agent-8A2BE2)

[English](README_EN.md)

## 核心能力

| Harness 能力 | 实现 |
| --- | --- |
| Agent Runtime | 流式响应、tool calling、轮次上限与安全终止 |
| 安全边界 | 工作区隔离、符号链接越界防护、写前读取、并发修改检测与人工审批 |
| 任务闭环 | 代码分析、变更执行、命令运行与最终验证 |
| 上下文管理 | JSONL 增量持久化、上下文压缩、Session 恢复与中断状态保留 |
| 可观测性 | 可选 JSONL trace，记录 Run、模型调用、工具执行和验证状态 |

## 快速开始

要求：Go 1.26、`rg` 和 DeepSeek API Key。

```bash
export DEEPSEEK_API_KEY="your-api-key"
go run ./cmd/go-coding-agent -root .
```

启动后可以直接提交代码任务：

```text
> 定位 NormalizeTag 的问题，修复后运行测试
```

CLI 只支持 TTY 交互。文件修改和命令执行会逐次请求确认。

## 会话与中断

| 输入 | 语义 |
| --- | --- |
| `Esc` | 中断当前 Run，保留 Session 并继续交互 |
| `Ctrl+C` | 中断当前 Run，结束 Session，输出 `sessionId` 并退出 |
| `/compact` | 压缩当前上下文，不结束 Session |
| `/new` | 结束当前 Session，输出旧 `sessionId`，然后创建新 Session |
| `/exit` | 结束当前 Session，输出 `sessionId` 并退出 |
| `-session <sessionId>` | 仅恢复已有 Session；新 Session ID 始终由程序自动生成 |

恢复 CLI 输出的 Session：

```bash
SESSION_ID="<printed-session-id>"
go run ./cmd/go-coding-agent -root . -session "$SESSION_ID"
```

## 其他用法

运行单次任务：

```bash
go run ./cmd/go-coding-agent -root . "分析项目入口并说明主要调用链"
```

记录执行轨迹：

```bash
go run ./cmd/go-coding-agent -root . -trace /tmp/go-coding-agent-trace.jsonl "分析项目入口"
```
