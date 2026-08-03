package tools

import (
	"context"
	"fmt"
	"github.com/goccy/go-json"
	"os"
	"strings"
)

// listFiles returns the direct children of a directory inside root.
func listFiles(_ context.Context, root, requested string) ([]string, error) {
	directory, err := resolveExistingPath(root, requested)
	if err != nil {
		return nil, fmt.Errorf("list files in %q: %w", requested, err)
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		return nil, fmt.Errorf("list files in %q: %w", requested, err)
	}

	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() {
			name += "/"
		}
		names = append(names, name)
	}

	return names, nil
}

type listFilesArguments struct {
	Path string `json:"path"`
}

// executeListFiles runs listFiles from JSON tool arguments.
func executeListFiles(ctx context.Context, root, arguments string) (string, error) {
	var input listFilesArguments
	decoder := json.NewDecoder(strings.NewReader(arguments))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		return "", fmt.Errorf("execute list_files: decode arguments: %w", err)
	}
	if input.Path == "" {
		return "", fmt.Errorf("execute list_files: path is empty")
	}

	files, err := listFiles(ctx, root, input.Path)
	if err != nil {
		return "", fmt.Errorf("execute list_files: %w", err)
	}
	result, err := json.Marshal(files)
	if err != nil {
		return "", fmt.Errorf("execute list_files: encode result: %w", err)
	}

	return string(result), nil
}
