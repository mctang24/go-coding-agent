package trace

import (
	"bufio"
	"github.com/goccy/go-json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWriterAppend(t *testing.T) {
	path := filepath.Join(t.TempDir(), "trace.jsonl")
	writer := Writer{Path: path}
	timestamp := time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC)
	events := []Event{
		{Timestamp: timestamp, SessionID: "session-1", RunID: "run-1", Type: "run_start", Data: map[string]any{"task": "inspect"}},
		{Timestamp: timestamp.Add(time.Second), SessionID: "session-1", RunID: "run-1", Type: "model_request", Turn: 1, DurationMS: 12},
	}

	for _, event := range events {
		if err := writer.Append(event); err != nil {
			t.Fatalf("Append() error = %v", err)
		}
	}

	file, err := os.Open(path)
	if err != nil {
		t.Fatalf("open trace: %v", err)
	}
	defer file.Close()

	var got []Event
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var event Event
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			t.Fatalf("decode trace line: %v", err)
		}
		got = append(got, event)
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan trace: %v", err)
	}
	if len(got) != len(events) {
		t.Fatalf("event count = %d, want %d", len(got), len(events))
	}
	if got[0].SessionID != "session-1" || got[0].RunID != "run-1" || got[0].Type != "run_start" || !got[0].Timestamp.Equal(timestamp) {
		t.Fatalf("first event = %#v", got[0])
	}
	if got[1].Type != "model_request" || got[1].Turn != 1 || got[1].DurationMS != 12 {
		t.Fatalf("second event = %#v", got[1])
	}

	badPath := filepath.Join(t.TempDir(), "bad.jsonl")
	badWriter := Writer{Path: badPath}
	if err := badWriter.Append(Event{SessionID: "session-1", RunID: "run-1", Type: "bad", Data: make(chan int)}); err == nil {
		t.Fatal("Append() error = nil, want JSON encoding error")
	}
	if _, err := os.Stat(badPath); !os.IsNotExist(err) {
		t.Fatalf("bad trace file exists or stat error = %v", err)
	}
	if err := writer.Append(Event{RunID: "run-1", Type: "run_start"}); err == nil {
		t.Fatal("Append() error = nil, want empty session ID error")
	}
	if err := writer.Append(Event{SessionID: "session-1", Type: "run_start"}); err == nil {
		t.Fatal("Append() error = nil, want empty run ID error")
	}
	if err := writer.Append(Event{SessionID: "session-1", Type: "session_reset"}); err != nil {
		t.Fatalf("Append() session_reset error = %v", err)
	}
}
