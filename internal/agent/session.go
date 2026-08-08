package agent

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/goccy/go-json"
	"github.com/gofrs/uuid/v5"
)

const sessionFormatVersion = 1

const sessionDirectoryName = ".go-coding-agent"

type sessionMeta struct {
	Type      string    `json:"type"`
	Version   int       `json:"version"`
	SessionID string    `json:"sessionId"`
	CreatedAt time.Time `json:"createdAt"`
	RootPath  string    `json:"rootPath"`
}

type sessionFile struct {
	id       string
	path     string
	rootPath string
}

// verificationFact is retained for replaying older Session records.
type verificationFact struct {
	Tool     string `json:"tool"`
	Command  string `json:"command"`
	ExitCode *int   `json:"exitCode,omitempty"`
	Error    string `json:"error,omitempty"`
}

type verificationState struct {
	HasUnverifiedChange bool              `json:"hasUnverifiedChange"`
	LastVerification    *verificationFact `json:"lastVerification,omitempty"`
}

type runCommitRecord struct {
	Type              string            `json:"type"`
	Messages          []Message         `json:"messages"`
	TokenUsage        int               `json:"tokenUsage"`
	VerificationState verificationState `json:"verificationState"`
}

type compactionRecord struct {
	Type               string            `json:"type"`
	ReplacementHistory []Message         `json:"replacementHistory"`
	TokenUsage         int               `json:"tokenUsage"`
	VerificationState  verificationState `json:"verificationState"`
}

type restoredSessionState struct {
	messages     []Message
	tokenUsage   int
	verification verificationState
}

// NewSessionAgent creates an Agent backed by a new or existing Session.
func NewSessionAgent(root string, model Model, instructions, sessionID string, maxTurns ...int) (*Agent, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("new session agent: resolve user home directory: %w", err)
	}
	return newSessionAgent(filepath.Join(home, sessionDirectoryName, "sessions"), root, model, instructions, sessionID, maxTurns...)
}

func newSessionAgent(sessionDir, root string, model Model, instructions, sessionID string, maxTurns ...int) (*Agent, error) {
	runner, err := NewAgent(root, model, instructions, maxTurns...)
	if err != nil {
		return nil, err
	}

	var file sessionFile
	var state restoredSessionState
	if sessionID == "" {
		file, err = newSessionFile(sessionDir, root)
	} else {
		file, state, err = restoreSessionFile(sessionDir, sessionID, root)
	}
	if err != nil {
		return nil, fmt.Errorf("new session agent: %w", err)
	}

	runner.sessionFile = &file
	runner.sessionID = file.id
	runner.messages = state.messages
	runner.tokenUsage = state.tokenUsage
	return runner, nil
}

func newSessionID() (string, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return "", fmt.Errorf("generate session ID: %w", err)
	}
	return id.String(), nil
}

func newSessionFile(sessionDir, root string) (created sessionFile, err error) {
	if sessionDir == "" {
		return sessionFile{}, fmt.Errorf("create session: directory is empty")
	}
	rootPath, err := normalizeRootPath(root)
	if err != nil {
		return sessionFile{}, fmt.Errorf("create session: %w", err)
	}
	sessionID, err := newSessionID()
	if err != nil {
		return sessionFile{}, fmt.Errorf("create session: %w", err)
	}
	encoded, err := json.Marshal(sessionMeta{
		Type:      "session_meta",
		Version:   sessionFormatVersion,
		SessionID: sessionID,
		CreatedAt: time.Now().UTC(),
		RootPath:  rootPath,
	})
	if err != nil {
		return sessionFile{}, fmt.Errorf("create session: encode metadata: %w", err)
	}
	encoded = append(encoded, '\n')

	if err := os.MkdirAll(sessionDir, 0o700); err != nil {
		return sessionFile{}, fmt.Errorf("create session: create directory %q: %w", sessionDir, err)
	}
	if err := os.Chmod(sessionDir, 0o700); err != nil {
		return sessionFile{}, fmt.Errorf("create session: secure directory %q: %w", sessionDir, err)
	}

	path := filepath.Join(sessionDir, sessionID+".jsonl")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return sessionFile{}, fmt.Errorf("create session: open file %q: %w", path, err)
	}
	defer func() {
		if closeErr := file.Close(); err == nil && closeErr != nil {
			created = sessionFile{}
			err = fmt.Errorf("create session: close file %q: %w", path, closeErr)
		}
	}()

	written, err := file.Write(encoded)
	if err != nil {
		return sessionFile{}, fmt.Errorf("create session: write metadata: %w", err)
	}
	if written != len(encoded) {
		return sessionFile{}, fmt.Errorf("create session: write metadata: %w", io.ErrShortWrite)
	}
	if err := file.Sync(); err != nil {
		return sessionFile{}, fmt.Errorf("create session: sync metadata: %w", err)
	}

	return sessionFile{id: sessionID, path: path, rootPath: rootPath}, nil
}

func (file sessionFile) appendRunCommit(messages []Message, tokenUsage int, verification verificationState) error {
	record := runCommitRecord{
		Type:              "run_commit",
		Messages:          messages,
		TokenUsage:        tokenUsage,
		VerificationState: verification,
	}
	if err := file.appendRecord(record); err != nil {
		return fmt.Errorf("append run commit: %w", err)
	}
	return nil
}

func (file sessionFile) appendCompaction(replacementHistory []Message, tokenUsage int, verification verificationState) error {
	record := compactionRecord{
		Type:               "compaction",
		ReplacementHistory: replacementHistory,
		TokenUsage:         tokenUsage,
		VerificationState:  verification,
	}
	if err := file.appendRecord(record); err != nil {
		return fmt.Errorf("append compaction: %w", err)
	}
	return nil
}

func (file sessionFile) appendRecord(record any) error {
	if file.id == "" {
		return fmt.Errorf("session ID is empty")
	}
	if file.path == "" {
		return fmt.Errorf("session path is empty")
	}
	if file.rootPath == "" {
		return fmt.Errorf("root path is empty")
	}
	encoded, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("encode record: %w", err)
	}
	encoded = append(encoded, '\n')

	opened, err := os.OpenFile(file.path, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		return fmt.Errorf("open session file %q: %w", file.path, err)
	}
	written, writeErr := opened.Write(encoded)
	if writeErr == nil && written != len(encoded) {
		writeErr = io.ErrShortWrite
	}
	if writeErr != nil {
		_ = opened.Close()
		return fmt.Errorf("write record: %w", writeErr)
	}
	if err := opened.Sync(); err != nil {
		_ = opened.Close()
		return fmt.Errorf("sync record: %w", err)
	}
	if err := opened.Close(); err != nil {
		return fmt.Errorf("close session file %q: %w", file.path, err)
	}
	return nil
}

func restoreSessionFile(sessionDir, sessionID, root string) (sessionFile, restoredSessionState, error) {
	if err := validateSessionID(sessionID); err != nil {
		return sessionFile{}, restoredSessionState{}, fmt.Errorf("restore session: %w", err)
	}
	rootPath, err := normalizeRootPath(root)
	if err != nil {
		return sessionFile{}, restoredSessionState{}, fmt.Errorf("restore session: %w", err)
	}

	path := filepath.Join(sessionDir, sessionID+".jsonl")
	content, err := os.ReadFile(path)
	if err != nil {
		return sessionFile{}, restoredSessionState{}, fmt.Errorf("restore session: read file %q: %w", path, err)
	}
	if len(content) == 0 {
		return sessionFile{}, restoredSessionState{}, fmt.Errorf("restore session: session file is empty")
	}
	lines := bytes.Split(content, []byte{'\n'})

	metadata, err := decodeSessionMetadata(lines[0])
	if err != nil {
		return sessionFile{}, restoredSessionState{}, fmt.Errorf("restore session: record 1: %w", err)
	}
	if metadata.Version != sessionFormatVersion {
		return sessionFile{}, restoredSessionState{}, fmt.Errorf("restore session: unsupported format version %d", metadata.Version)
	}
	if metadata.SessionID != sessionID {
		return sessionFile{}, restoredSessionState{}, fmt.Errorf("restore session: metadata session ID %q does not match %q", metadata.SessionID, sessionID)
	}
	if metadata.RootPath != rootPath {
		return sessionFile{}, restoredSessionState{}, fmt.Errorf("restore session: metadata root path %q does not match %q", metadata.RootPath, rootPath)
	}

	state := restoredSessionState{}
	for _, line := range lines[1:] {
		if len(line) == 0 {
			continue
		}
		var fields map[string]any
		if err := json.Unmarshal(line, &fields); err != nil {
			continue
		}
		recordType, ok := fields["type"].(string)
		if !ok {
			continue
		}
		switch recordType {
		case "run_commit":
			if !hasRequiredSessionFields(fields, "messages", "tokenUsage", "verificationState") {
				continue
			}
			var record runCommitRecord
			if err := json.Unmarshal(line, &record); err != nil {
				continue
			}
			state.messages = append(state.messages, record.Messages...)
			state.tokenUsage = record.TokenUsage
			state.verification = record.VerificationState
		case "compaction":
			if !hasRequiredSessionFields(fields, "replacementHistory", "tokenUsage", "verificationState") {
				continue
			}
			var record compactionRecord
			if err := json.Unmarshal(line, &record); err != nil {
				continue
			}
			state.messages = record.ReplacementHistory
			state.tokenUsage = record.TokenUsage
			state.verification = record.VerificationState
		default:
			continue
		}
	}

	if err := ensureSessionNewline(path, content); err != nil {
		return sessionFile{}, restoredSessionState{}, fmt.Errorf("restore session: %w", err)
	}
	return sessionFile{id: sessionID, path: path, rootPath: rootPath}, state, nil
}

func hasRequiredSessionFields(fields map[string]any, names ...string) bool {
	for _, name := range names {
		if value, ok := fields[name]; !ok || value == nil {
			return false
		}
	}
	return true
}

func decodeSessionMetadata(encoded []byte) (sessionMeta, error) {
	var metadata sessionMeta
	if err := json.Unmarshal(encoded, &metadata); err != nil {
		return sessionMeta{}, fmt.Errorf("decode metadata: %w", err)
	}
	if metadata.Type != "session_meta" {
		return sessionMeta{}, fmt.Errorf("expected session_meta, got %q", metadata.Type)
	}
	return metadata, nil
}

func validateSessionID(sessionID string) error {
	parsed, err := uuid.FromString(sessionID)
	if err != nil || parsed.String() != sessionID || parsed.Version() != uuid.V7 || parsed.Variant() != uuid.VariantRFC9562 {
		return fmt.Errorf("session ID %q is not a canonical UUIDv7", sessionID)
	}
	return nil
}

func ensureSessionNewline(path string, content []byte) error {
	if len(content) == 0 || content[len(content)-1] == '\n' {
		return nil
	}
	opened, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		return fmt.Errorf("open file for newline repair: %w", err)
	}
	if _, err := opened.WriteString("\n"); err != nil {
		_ = opened.Close()
		return fmt.Errorf("append missing newline: %w", err)
	}
	if err := opened.Sync(); err != nil {
		_ = opened.Close()
		return fmt.Errorf("sync missing newline: %w", err)
	}
	if err := opened.Close(); err != nil {
		return fmt.Errorf("close file after newline repair: %w", err)
	}
	return nil
}

func normalizeRootPath(root string) (string, error) {
	if root == "" {
		return "", fmt.Errorf("normalize root path: root is empty")
	}
	rootPath, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("normalize root path %q: %w", root, err)
	}
	rootPath, err = filepath.EvalSymlinks(rootPath)
	if err != nil {
		return "", fmt.Errorf("normalize root path %q: %w", root, err)
	}
	info, err := os.Stat(rootPath)
	if err != nil {
		return "", fmt.Errorf("normalize root path %q: %w", root, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("normalize root path %q: root is not a directory", root)
	}
	return rootPath, nil
}
