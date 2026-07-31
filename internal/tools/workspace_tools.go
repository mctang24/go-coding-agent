package tools

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
	"unicode/utf8"
)

type WriteRequest struct {
	Tool string
	Path string
}

type WriteApprover func(context.Context, WriteRequest) (bool, error)

type CommandRequest struct {
	Tool    string
	Command string
	Args    []string
}

type CommandApprover func(context.Context, CommandRequest) (bool, error)

type WorkspaceTools struct {
	readHashes      map[string][sha256.Size]byte
	writeApprover   WriteApprover
	commandApprover CommandApprover
	commandTimeout  time.Duration
}

func NewWorkspaceTools() *WorkspaceTools {
	return &WorkspaceTools{
		readHashes:     make(map[string][sha256.Size]byte),
		commandTimeout: defaultCommandTimeout,
	}
}

func (workspaceTools *WorkspaceTools) SetWriteApprover(approver WriteApprover) {
	workspaceTools.writeApprover = approver
}

func (workspaceTools *WorkspaceTools) SetCommandApprover(approver CommandApprover) {
	workspaceTools.commandApprover = approver
}

func (workspaceTools *WorkspaceTools) ResetReadState() {
	clear(workspaceTools.readHashes)
}

func (workspaceTools *WorkspaceTools) Definitions() []Tool {
	pathProperty := StringSchema("Path must be relative to the agent working directory. Use \".\" for the root. Do not use absolute paths.")
	return []Tool{
		{
			Name:        "list_files",
			Description: "List the direct children of a directory. Use this to find file names or understand directory structure; it does not search file contents.",
			Parameters:  ObjectSchema(map[string]Schema{"path": pathProperty}, "path"),
			Execute:     executeListFiles,
		},
		{
			Name:        "read_file",
			Description: "Read the complete contents of a file with line numbers. Line-number prefixes are display metadata and are not part of the file content. When multiple independent files are known, call read_file for all of them in the same tool round. Only the path parameter is supported; line ranges, offsets, and partial reads are not supported.",
			Parameters:  ObjectSchema(map[string]Schema{"path": pathProperty}, "path"),
			Execute:     workspaceTools.executeReadFile,
		},
		{
			Name:        "search_text",
			Description: "Search file contents for multiple regular expressions in one call and return 10 lines of context before and after each match. Use this to find symbols, function signatures, or code patterns. Put all independent patterns for the same path in one patterns array.",
			Parameters: ObjectSchema(map[string]Schema{
				"path":     pathProperty,
				"patterns": ArraySchema(StringSchema("Regular expression for matching file contents."), "Regular expressions to search together."),
			}, "path", "patterns"),
			Execute: executeSearchText,
		},
		Tool{
			Name:        "edit_file",
			Description: "Edit an existing file by replacing one exact, unique text match. Read the file first.",
			Parameters: ObjectSchema(map[string]Schema{
				"path":       pathProperty,
				"old_string": StringSchema("Exact text to replace; it must occur once."),
				"new_string": StringSchema("Replacement text."),
			}, "path", "old_string", "new_string"),
			Execute: workspaceTools.executeEditFile,
		},
		Tool{
			Name:        "write_file",
			Description: "Create a text file or replace an existing file. Read an existing file first.",
			Parameters: ObjectSchema(map[string]Schema{
				"path":    pathProperty,
				"content": StringSchema("Complete file content."),
			}, "path", "content"),
			Execute: workspaceTools.executeWriteFile,
		},
		{
			Name:        "run_command",
			Description: "Run a program directly in the agent working directory. Shell syntax, custom working directories, environment changes, background processes, and interactive commands are not supported.",
			Parameters: ObjectSchema(map[string]Schema{
				"command": StringSchema("Executable name or path."),
				"args":    ArraySchema(StringSchema(""), "Arguments passed directly to the executable."),
			}, "command", "args"),
			Execute: workspaceTools.executeRunCommand,
		},
		{
			Name:        "verify_command",
			Description: "Run a completion check directly in the agent working directory. Use this instead of run_command for tests, linters, builds, or other commands whose exit code verifies the completed work. It has the same execution restrictions and may still have side effects.",
			Parameters: ObjectSchema(map[string]Schema{
				"command": StringSchema("Executable name or path."),
				"args":    ArraySchema(StringSchema(""), "Arguments passed directly to the executable."),
			}, "command", "args"),
			Execute: workspaceTools.executeVerifyCommand,
		},
	}
}

func (workspaceTools *WorkspaceTools) loadUnchangedFile(root, requested string) (string, []byte, os.FileMode, error) {
	path, err := resolveExistingPath(root, requested)
	if err != nil {
		return "", nil, 0, err
	}
	expected, ok := workspaceTools.readHashes[path]
	if !ok {
		return "", nil, 0, fmt.Errorf("%q must be read before writing", requested)
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", nil, 0, fmt.Errorf("stat %q: %w", requested, err)
	}
	if !info.Mode().IsRegular() {
		return "", nil, 0, fmt.Errorf("%q is not a regular file", requested)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return "", nil, 0, fmt.Errorf("read %q: %w", requested, err)
	}
	if sha256.Sum256(content) != expected {
		return "", nil, 0, fmt.Errorf("%q changed since it was read", requested)
	}
	if !utf8.Valid(content) {
		return "", nil, 0, fmt.Errorf("%q is not valid UTF-8 text", requested)
	}
	return path, content, info.Mode().Perm(), nil
}

func (workspaceTools *WorkspaceTools) requireWriteApproval(ctx context.Context, tool, path string) error {
	if workspaceTools.writeApprover == nil {
		return fmt.Errorf("write approval is not configured")
	}
	approved, err := workspaceTools.writeApprover(ctx, WriteRequest{Tool: tool, Path: path})
	if err != nil {
		return fmt.Errorf("request approval: %w", err)
	}
	if !approved {
		return fmt.Errorf("user denied writing %q", path)
	}
	return nil
}

func decodeArguments(arguments string, target any) error {
	decoder := json.NewDecoder(strings.NewReader(arguments))
	decoder.DisallowUnknownFields()
	return decoder.Decode(target)
}
