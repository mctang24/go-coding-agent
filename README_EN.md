# Go Coding Agent

Built from scratch in Go for real-world software engineering, this Coding Agent Harness focuses on reliable execution, context management, and explicit safety boundaries.

[![Go](https://img.shields.io/badge/Go-1.26-00ADD8?logo=go)](https://go.dev/)
![Coding Agent](https://img.shields.io/badge/AI-Coding_Agent-8A2BE2)

[中文](README.md)

## Core capabilities

| Harness capability | Implementation |
| --- | --- |
| Agent runtime | Streaming responses, tool calling, bounded loops, and safe termination |
| Safety boundaries | Workspace isolation, symlink escape protection, read-before-write checks, conflict detection, and explicit approval |
| Task completion | Code analysis, changes, command execution, and final verification driven by `finish_task` |
| Context management | Incremental JSONL persistence, context compaction, Session recovery, and interruption state |
| Observability | Optional JSONL traces for agent runs, model calls, tool execution, and verification state |

## Quick start

Requirements: Go 1.26, `rg`, and a DeepSeek API key.

```bash
export DEEPSEEK_API_KEY="your-api-key"
go run ./cmd/go-coding-agent -root .
```

Submit coding tasks directly in interactive mode:

```text
> Find the bug in NormalizeTag, fix it, and run the tests
```

The CLI supports TTY interaction only. File changes and command execution require explicit approval.

## Session lifecycle

| Input | Behavior |
| --- | --- |
| `Esc` | Interrupt the current Run, preserve the Session, and continue interacting |
| `Ctrl+C` | Interrupt the current Run, end the Session, print its `sessionId`, and exit |
| `/compact` | Compact the current context without ending the Session |
| `/new` | End the current Session, print its `sessionId`, and create a new Session |
| `/exit` | End the current Session, print its `sessionId`, and exit |
| `-session <sessionId>` | Restore an existing Session; new Session IDs are always generated automatically |

Restore the Session ID printed by the CLI:

```bash
SESSION_ID="<printed-session-id>"
go run ./cmd/go-coding-agent -root . -session "$SESSION_ID"
```

## Other usage

Run a one-off task:

```bash
go run ./cmd/go-coding-agent -root . "Explain the main entry point and call flow"
```

Record an execution trace:

```bash
go run ./cmd/go-coding-agent -root . -trace /tmp/go-coding-agent-trace.jsonl "Explain the project entry point"
```
