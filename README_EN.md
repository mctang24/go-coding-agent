# Go Coding Agent

A coding agent built from scratch in Go and a working Harness Engineering implementation that combines an agent runtime, controlled tools, and a verification loop for reliable coding tasks.

[![Go](https://img.shields.io/badge/Go-1.26-00ADD8?logo=go)](https://go.dev/)
[![DeepSeek](https://img.shields.io/badge/LLM-DeepSeek-4D6BFE)](https://www.deepseek.com/)
![Coding Agent](https://img.shields.io/badge/AI-Coding_Agent-8A2BE2)

[中文](README.md)

## Highlights

- **Core Harness Engineering capabilities**: combines the agent runtime, controlled tools, safety boundaries, context management, completion verification, and tracing into a minimal working harness.
- **Complete coding workflow**: built-in tools for code search, file operations, and command execution, validated through an end-to-end bug-fix task.
- **Safe file modifications**: read-before-write enforcement, hash-based conflict detection, atomic replacement, workspace boundaries, symlink escape protection, and explicit approval before file changes or command execution.
- **Resumable sessions**: conversations, tool execution results, verification state, and compacted context are persisted incrementally, allowing work to continue later using a Session ID.

## Quick start

Requires Go 1.26, `rg`, and a DeepSeek API key.

```bash
export DEEPSEEK_API_KEY="your-api-key"
go run ./cmd/go-coding-agent -root .
```

Submit coding tasks directly in interactive mode:

```text
> Find the bug in NormalizeTag, fix it, and run the tests
> /new
> /exit
```

You can also run a one-off task:

```bash
go run ./cmd/go-coding-agent -root . "Explain the main entry point and call flow"
```

To record an execution trace, provide a trace file:

```bash
go run ./cmd/go-coding-agent -root . -trace /tmp/go-coding-agent-trace.jsonl "Explain the project entry point"
```

File changes and command execution require explicit terminal approval. Conversations are kept in memory and are not persisted across processes.
