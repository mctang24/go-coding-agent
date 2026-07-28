package tools

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"unicode/utf8"
)

type writeFileArguments struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

func (workspaceTools *WorkspaceTools) executeWriteFile(ctx context.Context, root, arguments string) (string, error) {
	var input writeFileArguments
	if err := decodeArguments(arguments, &input); err != nil {
		return "", fmt.Errorf("execute write_file: decode arguments: %w", err)
	}
	if input.Path == "" {
		return "", fmt.Errorf("execute write_file: path is empty")
	}
	if !utf8.ValidString(input.Content) {
		return "", fmt.Errorf("execute write_file: content is not valid UTF-8")
	}

	path, err := resolveWritablePath(root, input.Path)
	if err != nil {
		return "", fmt.Errorf("execute write_file: %w", err)
	}
	// New files are writable by the owner and readable by other users.
	mode := os.FileMode(0o644)
	created := true
	if _, err := os.Stat(path); err == nil {
		var current []byte
		path, current, mode, err = workspaceTools.loadUnchangedFile(root, input.Path)
		if err != nil {
			return "", fmt.Errorf("execute write_file: %w", err)
		}
		created = false
		if string(current) == input.Content {
			return "", fmt.Errorf("execute write_file: content does not change the file")
		}
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("execute write_file: stat %q: %w", input.Path, err)
	}
	if err := workspaceTools.requireWriteApproval(ctx, "write_file", input.Path); err != nil {
		return "", fmt.Errorf("execute write_file: %w", err)
	}
	if created {
		path, err = resolveWritablePath(root, input.Path)
		if err != nil {
			return "", fmt.Errorf("execute write_file: recheck path after approval: %w", err)
		}
		if _, err := os.Lstat(path); err == nil {
			return "", fmt.Errorf("execute write_file: %q was created before writing", input.Path)
		} else if !os.IsNotExist(err) {
			return "", fmt.Errorf("execute write_file: inspect %q before writing: %w", input.Path, err)
		}
	} else {
		path, _, mode, err = workspaceTools.loadUnchangedFile(root, input.Path)
		if err != nil {
			return "", fmt.Errorf("execute write_file: %w", err)
		}
	}
	// New directories must be searchable so their files remain accessible.
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", fmt.Errorf("execute write_file: create parent directories: %w", err)
	}
	if err := writeFileAtomically(path, []byte(input.Content), mode); err != nil {
		return "", fmt.Errorf("execute write_file: write %q: %w", input.Path, err)
	}
	workspaceTools.readHashes[path] = sha256.Sum256([]byte(input.Content))
	action := "Updated"
	if created {
		action = "Created"
	}
	return fmt.Sprintf("%s %s (%d bytes)", action, input.Path, len(input.Content)), nil
}

func writeFileAtomically(path string, content []byte, mode os.FileMode) error {
	temporary, err := os.CreateTemp(filepath.Dir(path), ".go-coding-agent-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if _, err := temporary.Write(content); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Chmod(mode); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, path)
}
