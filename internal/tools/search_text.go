package tools

import (
	"context"
	"errors"
	"fmt"
	"github.com/goccy/go-json"
	"os/exec"
	"path/filepath"
	"strings"
)

// searchText searches an existing path inside root with regular expressions.
func searchText(ctx context.Context, root, requested string, patterns []string) (string, error) {
	if len(patterns) == 0 {
		return "", fmt.Errorf("search patterns are empty")
	}
	for _, pattern := range patterns {
		if pattern == "" {
			return "", fmt.Errorf("search pattern is empty")
		}
	}

	safeRoot, err := resolveExistingPath(root, ".")
	if err != nil {
		return "", err
	}
	safePath, err := resolveExistingPath(root, requested)
	if err != nil {
		return "", err
	}
	target, err := filepath.Rel(safeRoot, safePath)
	if err != nil {
		return "", fmt.Errorf("make search path relative: %w", err)
	}

	args := []string{"--line-number", "--with-filename", "--no-heading", "--color=never", "--context=10"}
	for _, pattern := range patterns {
		args = append(args, "-e", pattern)
	}
	args = append(args, "--", target)
	command := exec.CommandContext(ctx, "rg", args...)
	command.Dir = safeRoot
	var output limitedOutput
	command.Stdout = &output
	err = command.Run()
	if err == nil {
		return output.String(), nil
	}

	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		return "", nil
	}
	return "", fmt.Errorf("search text: %w", err)
}

type searchTextArguments struct {
	Path     string   `json:"path"`
	Patterns []string `json:"patterns"`
}

// executeSearchText runs searchText from JSON tool arguments.
func executeSearchText(ctx context.Context, root, arguments string) (string, error) {
	var input searchTextArguments
	decoder := json.NewDecoder(strings.NewReader(arguments))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		return "", fmt.Errorf("execute search_text: decode arguments: %w", err)
	}
	if input.Path == "" {
		return "", fmt.Errorf("execute search_text: path is empty")
	}
	if len(input.Patterns) == 0 {
		return "", fmt.Errorf("execute search_text: patterns are empty")
	}

	result, err := searchText(ctx, root, input.Path, input.Patterns)
	if err != nil {
		return "", fmt.Errorf("execute search_text: %w", err)
	}

	return result, nil
}
