package agent

import (
	"context"
	"errors"
	"github.com/goccy/go-json"
	"go-coding-agent/internal/tools"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/gofrs/uuid/v5"
)

var sessionIDPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

func TestNewSessionID(t *testing.T) {
	first, err := newSessionID()
	if err != nil {
		t.Fatalf("newSessionID() error = %v", err)
	}
	second, err := newSessionID()
	if err != nil {
		t.Fatalf("newSessionID() error = %v", err)
	}
	if !sessionIDPattern.MatchString(first) || !sessionIDPattern.MatchString(second) {
		t.Fatalf("session IDs = %q, %q", first, second)
	}
	if first == second {
		t.Fatalf("session IDs are equal: %q", first)
	}

	parsed, err := uuid.FromString(first)
	if err != nil {
		t.Fatalf("parse session ID %q: %v", first, err)
	}
	if parsed.Version() != uuid.V7 || parsed.Variant() != uuid.VariantRFC9562 {
		t.Fatalf("session ID %q version = %d, variant = %d", first, parsed.Version(), parsed.Variant())
	}
}

func TestNewSessionFile(t *testing.T) {
	tempDir := t.TempDir()
	root := filepath.Join(tempDir, "root")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	sessionDir := filepath.Join(tempDir, "sessions")
	if err := os.Mkdir(sessionDir, 0o755); err != nil {
		t.Fatal(err)
	}

	created, err := newSessionFile(sessionDir, root)
	if err != nil {
		t.Fatalf("newSessionFile() error = %v", err)
	}
	if !sessionIDPattern.MatchString(created.id) {
		t.Fatalf("session ID = %q", created.id)
	}
	wantRootPath, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	if created.rootPath != wantRootPath || created.path != filepath.Join(sessionDir, created.id+".jsonl") {
		t.Fatalf("session file = %#v", created)
	}

	directoryInfo, err := os.Stat(sessionDir)
	if err != nil {
		t.Fatal(err)
	}
	if got := directoryInfo.Mode().Perm(); got != 0o700 {
		t.Fatalf("session directory mode = %o, want 700", got)
	}
	fileInfo, err := os.Stat(created.path)
	if err != nil {
		t.Fatal(err)
	}
	if got := fileInfo.Mode().Perm(); got != 0o600 {
		t.Fatalf("session file mode = %o, want 600", got)
	}

	content, err := os.ReadFile(created.path)
	if err != nil {
		t.Fatal(err)
	}
	if len(content) == 0 || content[len(content)-1] != '\n' || strings.Count(string(content), "\n") != 1 {
		t.Fatalf("session file content = %q", content)
	}
	var metadata sessionMeta
	if err := json.Unmarshal(content, &metadata); err != nil {
		t.Fatalf("decode session metadata: %v", err)
	}
	if metadata.Type != "session_meta" || metadata.Version != sessionFormatVersion || metadata.SessionID != created.id || metadata.RootPath != wantRootPath || metadata.CreatedAt.IsZero() {
		t.Fatalf("session metadata = %#v", metadata)
	}
}

func TestNewSessionFileErrors(t *testing.T) {
	tempDir := t.TempDir()
	root := filepath.Join(tempDir, "root")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	notDirectory := filepath.Join(tempDir, "file")
	if err := os.WriteFile(notDirectory, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name       string
		sessionDir string
		root       string
		wantErr    string
	}{
		{name: "empty session directory", root: root, wantErr: "directory is empty"},
		{name: "empty root", sessionDir: filepath.Join(tempDir, "sessions"), wantErr: "root is empty"},
		{name: "missing root", sessionDir: filepath.Join(tempDir, "sessions"), root: filepath.Join(tempDir, "missing"), wantErr: "normalize root path"},
		{name: "root is a file", sessionDir: filepath.Join(tempDir, "sessions"), root: notDirectory, wantErr: "root is not a directory"},
		{name: "session directory is a file", sessionDir: notDirectory, root: root, wantErr: "create directory"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := newSessionFile(test.sessionDir, test.root); err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("newSessionFile() error = %v, want to contain %q", err, test.wantErr)
			}
		})
	}
}

func TestAppendAndRestoreSessionFile(t *testing.T) {
	tempDir := t.TempDir()
	root := filepath.Join(tempDir, "root")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	created, err := newSessionFile(filepath.Join(tempDir, "sessions"), root)
	if err != nil {
		t.Fatal(err)
	}

	firstRun := []Message{
		{Role: "user", Content: "inspect"},
		{
			Role:       "assistant",
			ToolCalls:  []ToolCall{{ID: "call_1", Name: "search_text", Arguments: `{"query":"target"}`}},
			RawMessage: json.RawMessage(`{"role":"assistant","content":null,"tool_calls":[{"id":"call_1","type":"function","function":{"name":"search_text","arguments":"{\"query\":\"target\"}"}}]}`),
		},
		{Role: "tool", ToolResults: []ToolResult{{ToolCallID: "call_1", Content: "found"}}},
		{Role: "assistant", Content: "found it", RawMessage: json.RawMessage(`{"role":"assistant","content":"found it"}`)},
	}
	if err := created.appendRunCommit(firstRun, 12, verificationState{HasUnverifiedChange: true}); err != nil {
		t.Fatalf("appendRunCommit() error = %v", err)
	}

	exitCode := 0
	compacted := []Message{
		{Role: "user", Content: summaryPrefix + validSummary()},
		{Role: "assistant", Content: "continue", RawMessage: json.RawMessage(`{"role":"assistant","content":"continue"}`)},
	}
	verified := verificationState{
		LastVerification: &verificationFact{Tool: "finish_task", Command: "go test ./...", ExitCode: &exitCode},
	}
	if err := created.appendCompaction(compacted, 7, verified); err != nil {
		t.Fatalf("appendCompaction() error = %v", err)
	}

	secondRun := []Message{
		{Role: "user", Content: "next"},
		{Role: "assistant", Content: "done", RawMessage: json.RawMessage(`{"role":"assistant","content":"done"}`)},
	}
	if err := created.appendRunCommit(secondRun, 10, verified); err != nil {
		t.Fatalf("appendRunCommit() second error = %v", err)
	}

	restoredFile, state, err := restoreSessionFile(filepath.Dir(created.path), created.id, root)
	if err != nil {
		t.Fatalf("restoreSessionFile() error = %v", err)
	}
	wantMessages := append(append([]Message(nil), compacted...), secondRun...)
	if restoredFile != created {
		t.Fatalf("restored file = %#v, want %#v", restoredFile, created)
	}
	if !reflect.DeepEqual(state.messages, wantMessages) || state.tokenUsage != 10 || !reflect.DeepEqual(state.verification, verified) {
		t.Fatalf("restored state = %#v", state)
	}

	content, err := os.ReadFile(created.path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(string(content), "\n"), "\n")
	if len(lines) != 4 {
		t.Fatalf("record count = %d, want 4", len(lines))
	}
	wantTypes := []string{"session_meta", "run_commit", "compaction", "run_commit"}
	for index, line := range lines {
		var header struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal([]byte(line), &header); err != nil {
			t.Fatalf("decode record %d: %v", index+1, err)
		}
		if header.Type != wantTypes[index] {
			t.Fatalf("record %d type = %q, want %q", index+1, header.Type, wantTypes[index])
		}
	}
}

func TestRestoreSessionFileSkipsIncompleteTail(t *testing.T) {
	tempDir := t.TempDir()
	root := filepath.Join(tempDir, "root")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	created, err := newSessionFile(filepath.Join(tempDir, "sessions"), root)
	if err != nil {
		t.Fatal(err)
	}
	firstRun := []Message{{Role: "user", Content: "first"}, {Role: "assistant", Content: "done"}}
	if err := created.appendRunCommit(firstRun, 5, verificationState{}); err != nil {
		t.Fatal(err)
	}
	committed, err := os.ReadFile(created.path)
	if err != nil {
		t.Fatal(err)
	}
	const fragment = `{"type":"run_commit"`
	opened, err := os.OpenFile(created.path, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := opened.WriteString(fragment); err != nil {
		_ = opened.Close()
		t.Fatal(err)
	}
	if err := opened.Close(); err != nil {
		t.Fatal(err)
	}

	restored, state, err := restoreSessionFile(filepath.Dir(created.path), created.id, root)
	if err != nil {
		t.Fatalf("restoreSessionFile() error = %v", err)
	}
	if !reflect.DeepEqual(state.messages, firstRun) || state.tokenUsage != 5 {
		t.Fatalf("restored state = %#v", state)
	}
	afterRepair, err := os.ReadFile(created.path)
	if err != nil {
		t.Fatal(err)
	}
	wantContent := append(append([]byte(nil), committed...), fragment...)
	wantContent = append(wantContent, '\n')
	if !reflect.DeepEqual(afterRepair, wantContent) {
		t.Fatalf("repaired content = %q, want %q", afterRepair, wantContent)
	}

	secondRun := []Message{{Role: "user", Content: "second"}, {Role: "assistant", Content: "continued"}}
	if err := restored.appendRunCommit(secondRun, 9, verificationState{}); err != nil {
		t.Fatalf("append after restore: %v", err)
	}
	_, continued, err := restoreSessionFile(filepath.Dir(created.path), created.id, root)
	if err != nil {
		t.Fatalf("restore after append: %v", err)
	}
	wantMessages := append(append([]Message(nil), firstRun...), secondRun...)
	if !reflect.DeepEqual(continued.messages, wantMessages) || continued.tokenUsage != 9 {
		t.Fatalf("continued state = %#v", continued)
	}
}

func TestAppendSessionRecordErrors(t *testing.T) {
	tempDir := t.TempDir()
	root := filepath.Join(tempDir, "root")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	created, err := newSessionFile(filepath.Join(tempDir, "sessions"), root)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name    string
		append  func() error
		wantErr string
	}{
		{
			name: "unencodable record",
			append: func() error {
				return created.appendRecord(make(chan int))
			},
			wantErr: "encode record",
		},
		{
			name: "missing session file",
			append: func() error {
				missing := created
				missing.path = filepath.Join(tempDir, "missing.jsonl")
				return missing.appendRunCommit(nil, 1, verificationState{})
			},
			wantErr: "open session file",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := test.append(); err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("append error = %v, want to contain %q", err, test.wantErr)
			}
		})
	}
}

func TestRestoreSessionFileErrors(t *testing.T) {
	t.Run("invalid session ID", func(t *testing.T) {
		if _, _, err := restoreSessionFile(t.TempDir(), "session_bad", t.TempDir()); err == nil || !strings.Contains(err.Error(), "canonical UUIDv7") {
			t.Fatalf("restoreSessionFile() error = %v", err)
		}
	})

	t.Run("invalid metadata", func(t *testing.T) {
		created, sessionDir, root, _ := newSessionFixture(t)
		if err := os.WriteFile(created.path, []byte("{\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, _, err := restoreSessionFile(sessionDir, created.id, root); err == nil || !strings.Contains(err.Error(), "record 1") {
			t.Fatalf("restoreSessionFile() error = %v", err)
		}
	})

	t.Run("root mismatch", func(t *testing.T) {
		created, sessionDir, _, otherRoot := newSessionFixture(t)
		if _, _, err := restoreSessionFile(sessionDir, created.id, otherRoot); err == nil || !strings.Contains(err.Error(), "root path") {
			t.Fatalf("restoreSessionFile() error = %v", err)
		}
	})

	t.Run("broader permissions", func(t *testing.T) {
		created, sessionDir, root, _ := newSessionFixture(t)
		if err := os.Chmod(sessionDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(created.path, 0o644); err != nil {
			t.Fatal(err)
		}
		if _, _, err := restoreSessionFile(sessionDir, created.id, root); err != nil {
			t.Fatalf("restoreSessionFile() error = %v", err)
		}
	})

	t.Run("unsupported version", func(t *testing.T) {
		created, sessionDir, root, _ := newSessionFixture(t)
		content, err := os.ReadFile(created.path)
		if err != nil {
			t.Fatal(err)
		}
		var metadata sessionMeta
		if err := json.Unmarshal(content, &metadata); err != nil {
			t.Fatal(err)
		}
		metadata.Version = sessionFormatVersion + 1
		encoded, err := json.Marshal(metadata)
		if err != nil {
			t.Fatal(err)
		}
		encoded = append(encoded, '\n')
		if err := os.WriteFile(created.path, encoded, 0o600); err != nil {
			t.Fatal(err)
		}
		if _, _, err := restoreSessionFile(sessionDir, created.id, root); err == nil || !strings.Contains(err.Error(), "unsupported format version") {
			t.Fatalf("restoreSessionFile() error = %v", err)
		}
	})

	t.Run("metadata without newline", func(t *testing.T) {
		created, sessionDir, root, _ := newSessionFixture(t)
		content, err := os.ReadFile(created.path)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(created.path, content[:len(content)-1], 0o600); err != nil {
			t.Fatal(err)
		}
		if _, _, err := restoreSessionFile(sessionDir, created.id, root); err != nil {
			t.Fatalf("restoreSessionFile() error = %v", err)
		}
		repaired, err := os.ReadFile(created.path)
		if err != nil {
			t.Fatal(err)
		}
		if len(repaired) == 0 || repaired[len(repaired)-1] != '\n' {
			t.Fatalf("repaired content = %q", repaired)
		}
	})
}

func TestRestoreSessionFileSkipsInvalidRecords(t *testing.T) {
	for _, test := range []struct {
		name string
		line string
	}{
		{name: "empty record"},
		{name: "corrupt record", line: `{`},
		{name: "unknown record type", line: `{"type":"future"}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			created, sessionDir, root, _ := newSessionFixture(t)
			appendSessionTestLine(t, created.path, test.line)
			wantMessages := []Message{{Role: "user", Content: "after invalid record"}}
			if err := created.appendRunCommit(wantMessages, 7, verificationState{}); err != nil {
				t.Fatal(err)
			}

			_, state, err := restoreSessionFile(sessionDir, created.id, root)
			if err != nil {
				t.Fatalf("restoreSessionFile() error = %v", err)
			}
			if !reflect.DeepEqual(state.messages, wantMessages) || state.tokenUsage != 7 {
				t.Fatalf("restored state = %#v", state)
			}
		})
	}
}

func TestRestoreSessionFileSkipsIncompleteKnownRecords(t *testing.T) {
	for _, test := range []struct {
		name string
		line string
	}{
		{name: "run commit without messages", line: `{"type":"run_commit","tokenUsage":9,"verificationState":{"hasUnverifiedChange":false}}`},
		{name: "run commit with null messages", line: `{"type":"run_commit","messages":null,"tokenUsage":9,"verificationState":{"hasUnverifiedChange":false}}`},
		{name: "run commit without token usage", line: `{"type":"run_commit","messages":[],"verificationState":{"hasUnverifiedChange":false}}`},
		{name: "run commit without verification state", line: `{"type":"run_commit","messages":[],"tokenUsage":9}`},
		{name: "compaction without replacement history", line: `{"type":"compaction","tokenUsage":9,"verificationState":{"hasUnverifiedChange":false}}`},
		{name: "compaction without token usage", line: `{"type":"compaction","replacementHistory":[],"verificationState":{"hasUnverifiedChange":false}}`},
		{name: "compaction without verification state", line: `{"type":"compaction","replacementHistory":[],"tokenUsage":9}`},
		{name: "compaction with null verification state", line: `{"type":"compaction","replacementHistory":[],"tokenUsage":9,"verificationState":null}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			created, sessionDir, root, _ := newSessionFixture(t)
			wantMessages := []Message{{Role: "user", Content: "committed"}}
			wantVerification := verificationState{HasUnverifiedChange: true}
			if err := created.appendRunCommit(wantMessages, 7, wantVerification); err != nil {
				t.Fatal(err)
			}
			appendSessionTestLine(t, created.path, test.line)

			_, state, err := restoreSessionFile(sessionDir, created.id, root)
			if err != nil {
				t.Fatalf("restoreSessionFile() error = %v", err)
			}
			if !reflect.DeepEqual(state.messages, wantMessages) || state.tokenUsage != 7 || !reflect.DeepEqual(state.verification, wantVerification) {
				t.Fatalf("restored state = %#v", state)
			}
		})
	}
}

func TestRestoreSessionFileIgnoresUnknownFields(t *testing.T) {
	created, sessionDir, root, _ := newSessionFixture(t)
	wantMessages := []Message{{Role: "user", Content: "committed"}}
	if err := created.appendRunCommit(wantMessages, 7, verificationState{}); err != nil {
		t.Fatal(err)
	}
	appendSessionTestLine(t, created.path, `{"type":"run_commit","messages":[],"tokenUsage":9,"verificationState":{"hasUnverifiedChange":true,"futureState":true},"futureRecord":true}`)

	_, state, err := restoreSessionFile(sessionDir, created.id, root)
	if err != nil {
		t.Fatalf("restoreSessionFile() error = %v", err)
	}
	if !reflect.DeepEqual(state.messages, wantMessages) || state.tokenUsage != 9 || !state.verification.HasUnverifiedChange {
		t.Fatalf("restored state = %#v", state)
	}
}

func TestSessionAgentRestoresAndContinues(t *testing.T) {
	sessionDir := t.TempDir()
	root := t.TempDir()
	firstModel := &modelStub{responses: []ModelResponse{{
		Message: Message{Role: "assistant", Content: "first answer"},
		Usage:   TokenUsage{TotalTokens: 12},
	}}}
	created, err := newSessionAgent(sessionDir, root, firstModel, "inspect", "")
	if err != nil {
		t.Fatalf("newSessionAgent() error = %v", err)
	}
	exitCode := 0
	created.hasUnverifiedChange = true
	created.lastVerification = &verificationFact{Tool: "finish_task", ExitCode: &exitCode}
	if _, err := created.Run(context.Background(), "first question", nil); err != nil {
		t.Fatalf("first Run() error = %v", err)
	}

	secondModel := &modelStub{responses: []ModelResponse{{
		Message: Message{Role: "assistant", Content: "second answer"},
		Usage:   TokenUsage{TotalTokens: 20},
	}}}
	restored, err := newSessionAgent(sessionDir, root, secondModel, "inspect", created.SessionID())
	if err != nil {
		t.Fatalf("restore newSessionAgent() error = %v", err)
	}
	if restored.tokenUsage != 12 || len(restored.messages) != 2 || !restored.hasUnverifiedChange || !reflect.DeepEqual(restored.lastVerification, created.lastVerification) {
		t.Fatalf("restored state = messages %#v, token usage %d", restored.messages, restored.tokenUsage)
	}
	if _, err := restored.Run(context.Background(), "second question", nil); err != nil {
		t.Fatalf("second Run() error = %v", err)
	}
	requestMessages := secondModel.requests[0].Messages
	if len(requestMessages) != 3 || requestMessages[0].Content != "first question" || requestMessages[1].Content != "first answer" || requestMessages[2].Content != "second question" {
		t.Fatalf("restored request messages = %#v", requestMessages)
	}

	_, state, err := restoreSessionFile(sessionDir, created.SessionID(), root)
	if err != nil {
		t.Fatalf("final restoreSessionFile() error = %v", err)
	}
	if len(state.messages) != 4 || state.tokenUsage != 20 {
		t.Fatalf("final state = messages %#v, token usage %d", state.messages, state.tokenUsage)
	}
}

func TestSessionAgentRestoresCompletedToolRoundAfterModelFailure(t *testing.T) {
	sessionDir := t.TempDir()
	root := t.TempDir()
	modelErr := errors.New("second request failed")
	model := &modelStub{
		responses: []ModelResponse{{
			Message: Message{Role: "assistant", ToolCalls: []ToolCall{{ID: "write_1", Name: "write_file", Arguments: `{}`}}},
			Usage:   TokenUsage{TotalTokens: 31},
		}},
		errs: []error{nil, modelErr},
	}
	runner, err := newSessionAgent(sessionDir, root, model, "", "", 2)
	if err != nil {
		t.Fatalf("newSessionAgent() error = %v", err)
	}
	runner.tools = []tools.Tool{{Name: "write_file", Execute: func(context.Context, string, string) (string, error) {
		return `{"path":"created.txt"}`, nil
	}}}

	if _, err := runner.Run(context.Background(), "create file", nil); !errors.Is(err, modelErr) {
		t.Fatalf("Run() error = %v, want model error", err)
	}
	restored, err := newSessionAgent(sessionDir, root, &modelStub{}, "", runner.SessionID())
	if err != nil {
		t.Fatalf("restore newSessionAgent() error = %v", err)
	}
	if len(restored.messages) != 3 || restored.messages[0].Content != "create file" || restored.messages[2].ToolResults[0].Content != `{"path":"created.txt"}` {
		t.Fatalf("restored messages = %#v", restored.messages)
	}
	if restored.tokenUsage != 31 || !restored.hasUnverifiedChange || restored.lastVerification != nil {
		t.Fatalf("restored state = token usage %d, unverified %t, verification %#v", restored.tokenUsage, restored.hasUnverifiedChange, restored.lastVerification)
	}
}

func TestSessionAgentPersistsCompaction(t *testing.T) {
	sessionDir := t.TempDir()
	root := t.TempDir()
	model := &compactSequenceModel{
		responses:       []ModelResponse{{Message: Message{Role: "assistant", Content: validCompactSummary()}}},
		estimatedTokens: 40,
	}
	runner, err := newSessionAgent(sessionDir, root, model, "", "")
	if err != nil {
		t.Fatalf("newSessionAgent() error = %v", err)
	}
	runner.messages = compactableHistory()
	if err := runner.sessionFile.appendRunCommit(runner.messages, 90, verificationState{}); err != nil {
		t.Fatalf("seed Session error = %v", err)
	}

	if err := runner.Compact(context.Background()); err != nil {
		t.Fatalf("Compact() error = %v", err)
	}
	_, state, err := restoreSessionFile(sessionDir, runner.SessionID(), root)
	if err != nil {
		t.Fatalf("restoreSessionFile() error = %v", err)
	}
	if !reflect.DeepEqual(state.messages, runner.messages) || state.tokenUsage != 40 {
		t.Fatalf("restored compacted state = messages %#v, token usage %d", state.messages, state.tokenUsage)
	}
}

func TestSessionAgentResetCreatesNewSession(t *testing.T) {
	sessionDir := t.TempDir()
	root := t.TempDir()
	runner, err := newSessionAgent(sessionDir, root, &modelStub{}, "", "")
	if err != nil {
		t.Fatalf("newSessionAgent() error = %v", err)
	}
	endedSessionID := runner.SessionID()

	if err := runner.Reset(); err != nil {
		t.Fatalf("Reset() error = %v", err)
	}
	if runner.SessionID() == endedSessionID || !sessionIDPattern.MatchString(runner.SessionID()) {
		t.Fatalf("Session ID after Reset() = %q", runner.SessionID())
	}
	if _, _, err := restoreSessionFile(sessionDir, endedSessionID, root); err != nil {
		t.Fatalf("restore ended Session error = %v", err)
	}
	if _, _, err := restoreSessionFile(sessionDir, runner.SessionID(), root); err != nil {
		t.Fatalf("restore new Session error = %v", err)
	}
}

func TestSessionAgentReportsCommitFailure(t *testing.T) {
	sessionDir := t.TempDir()
	root := t.TempDir()
	model := &modelStub{responses: []ModelResponse{{Message: Message{Role: "assistant", Content: "answer"}}}}
	runner, err := newSessionAgent(sessionDir, root, model, "", "")
	if err != nil {
		t.Fatalf("newSessionAgent() error = %v", err)
	}
	originalFile := *runner.sessionFile
	brokenFile := originalFile
	brokenFile.path = filepath.Join(sessionDir, "missing", "session.jsonl")
	runner.sessionFile = &brokenFile

	if _, err := runner.Run(context.Background(), "question", nil); err == nil || !strings.Contains(err.Error(), "persist session") {
		t.Fatalf("Run() error = %v, want persistence failure", err)
	}
	if len(runner.messages) != 0 {
		t.Fatalf("messages = %#v, want no in-memory commit", runner.messages)
	}
	_, state, err := restoreSessionFile(sessionDir, originalFile.id, root)
	if err != nil {
		t.Fatalf("restoreSessionFile() error = %v", err)
	}
	if len(state.messages) != 0 {
		t.Fatalf("persisted messages = %#v, want none", state.messages)
	}
}

func TestSessionAgentDoesNotContinueAfterToolRoundCommitFailure(t *testing.T) {
	sessionDir := t.TempDir()
	root := t.TempDir()
	model := &modelStub{responses: []ModelResponse{
		{Message: Message{Role: "assistant", ToolCalls: []ToolCall{{ID: "echo_1", Name: "echo", Arguments: `{}`}}}},
		{Message: Message{Role: "assistant", Content: "must not be requested"}},
	}}
	runner, err := newSessionAgent(sessionDir, root, model, "", "", 2)
	if err != nil {
		t.Fatalf("newSessionAgent() error = %v", err)
	}
	runner.tools = []tools.Tool{{Name: "echo", Execute: func(context.Context, string, string) (string, error) {
		return "done", nil
	}}}
	originalFile := *runner.sessionFile
	brokenFile := originalFile
	brokenFile.path = filepath.Join(sessionDir, "missing", "session.jsonl")
	runner.sessionFile = &brokenFile

	if _, err := runner.Run(context.Background(), "question", nil); err == nil || !strings.Contains(err.Error(), "persist session") {
		t.Fatalf("Run() error = %v, want persistence failure", err)
	}
	if len(model.requests) != 1 {
		t.Fatalf("model requests = %d, want no request after persistence failure", len(model.requests))
	}
	if len(runner.messages) != 0 {
		t.Fatalf("messages = %#v, want no in-memory commit", runner.messages)
	}
	_, state, err := restoreSessionFile(sessionDir, originalFile.id, root)
	if err != nil {
		t.Fatalf("restoreSessionFile() error = %v", err)
	}
	if len(state.messages) != 0 {
		t.Fatalf("persisted messages = %#v, want none", state.messages)
	}
}

func validCompactSummary() string {
	return "## 目标\n## 约束和偏好\n## 进度\n### 已完成\n### 进行中\n### 阻塞\n## 关键决策\n## 下一步\n## 关键上下文"
}

func newSessionFixture(t *testing.T) (sessionFile, string, string, string) {
	t.Helper()
	tempDir := t.TempDir()
	root := filepath.Join(tempDir, "root")
	otherRoot := filepath.Join(tempDir, "other")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(otherRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	sessionDir := filepath.Join(tempDir, "sessions")
	created, err := newSessionFile(sessionDir, root)
	if err != nil {
		t.Fatal(err)
	}
	return created, sessionDir, root, otherRoot
}

func appendSessionTestLine(t *testing.T, path, line string) {
	t.Helper()
	opened, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := opened.WriteString(line + "\n"); err != nil {
		_ = opened.Close()
		t.Fatal(err)
	}
	if err := opened.Close(); err != nil {
		t.Fatal(err)
	}
}
