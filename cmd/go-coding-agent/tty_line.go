package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	"golang.org/x/sys/unix"
	"golang.org/x/term"
)

func readTTYLine(ctx context.Context, fd int, output io.Writer) (string, error) {
	state, err := term.MakeRaw(fd)
	if err != nil {
		return "", fmt.Errorf("enable TTY input: %w", err)
	}
	defer func() { _ = term.Restore(fd, state) }()
	if err := enableTTYSignalsAndOutput(fd); err != nil {
		return "", fmt.Errorf("configure TTY input: %w", err)
	}

	var line []rune
	var pending []byte
	var escaped bool
	for {
		if ctx.Err() != nil {
			return "", context.Cause(ctx)
		}
		ready, err := pollTTYInput(fd)
		if err != nil {
			return "", err
		}
		if !ready {
			continue
		}
		var buffer [32]byte
		count, err := unix.Read(fd, buffer[:])
		if err != nil {
			if errors.Is(err, unix.EINTR) || errors.Is(err, unix.EAGAIN) {
				continue
			}
			return "", err
		}
		for _, b := range buffer[:count] {
			if escaped {
				if b >= ttyEscapeSequenceStart && b <= ttyEscapeSequenceEnd {
					escaped = false
				}
				continue
			}
			switch b {
			case ttyEscape:
				escaped = true
			case ttyCarriageReturn, ttyLineFeed:
				_, _ = io.WriteString(output, "\n")
				return string(line), nil
			case ttyBackspace, ttyDelete:
				if len(line) > 0 {
					width := ttyRuneWidth(line[len(line)-1])
					line = line[:len(line)-1]
					_, _ = io.WriteString(output, strings.Repeat("\b", width)+strings.Repeat(" ", width)+strings.Repeat("\b", width))
				}
			default:
				if b < ttyControlByteLimit {
					continue
				}
				pending = append(pending, b)
				if !utf8.FullRune(pending) {
					continue
				}
				runeValue, size := utf8.DecodeRune(pending)
				if runeValue == utf8.RuneError && size == 1 {
					pending = nil
					continue
				}
				line = append(line, runeValue)
				_, _ = output.Write(pending)
				pending = pending[size:]
			}
		}
	}
}

func ttyRuneWidth(value rune) int {
	if value >= ttyWideRuneStart {
		return 2
	}
	return 1
}
