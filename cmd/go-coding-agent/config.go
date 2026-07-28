package main

import (
	"flag"
	"fmt"
	"strings"
)

type config struct {
	root      string
	tracePath string
	task      string
}

// parseArgs parses the working directory and task.
func parseArgs(args []string) (config, error) {
	flags := flag.NewFlagSet("go-coding-agent", flag.ContinueOnError)
	root := flags.String("root", ".", "agent working directory")
	tracePath := flags.String("trace", "", "JSONL trace output path")
	if err := flags.Parse(args); err != nil {
		return config{}, fmt.Errorf("parse arguments: %w", err)
	}
	task := strings.Join(flags.Args(), " ")
	return config{root: *root, tracePath: *tracePath, task: task}, nil
}
