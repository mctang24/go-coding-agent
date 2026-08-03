package tools

import (
	"context"
	"crypto/sha256"
	"fmt"
	"github.com/goccy/go-json"
	"os"
	"strings"
)

// readFile reads an existing file inside root and returns its resolved path.
func readFile(_ context.Context, root, requested string) (string, string, error) {
	path, err := resolveExistingPath(root, requested)
	if err != nil {
		return "", "", err
	}

	content, err := os.ReadFile(path)
	if err != nil {
		return "", "", fmt.Errorf("read file %q: %w", requested, err)
	}

	return path, string(content), nil
}

type readFileArguments struct {
	Path string `json:"path"`
}

// executeReadFile runs readFile from JSON tool arguments.
func (workspaceTools *WorkspaceTools) executeReadFile(ctx context.Context, root, arguments string) (string, error) {
	var input readFileArguments
	decoder := json.NewDecoder(strings.NewReader(arguments))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		return "", fmt.Errorf("execute read_file: decode arguments: %w", err)
	}
	if input.Path == "" {
		return "", fmt.Errorf("execute read_file: path is empty")
	}

	path, content, err := readFile(ctx, root, input.Path)
	if err != nil {
		return "", fmt.Errorf("execute read_file: %w", err)
	}
	workspaceTools.readHashes[path] = sha256.Sum256([]byte(content))

	lines := strings.Split(content, "\n")
	for index := range lines {
		lines[index] = fmt.Sprintf("%4d | %s", index+1, lines[index])
	}
	return strings.Join(lines, "\n"), nil
}
