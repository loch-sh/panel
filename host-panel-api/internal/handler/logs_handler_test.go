package handler

import (
	"testing"
)

func TestParseLogLine_WithTimestamp(t *testing.T) {
	line := "2024-01-15T10:30:00.123456789Z Hello world"

	entry := parseLogLine(line)

	if entry.Time != "2024-01-15T10:30:00.123456789Z" {
		t.Errorf("Time = %q, want %q", entry.Time, "2024-01-15T10:30:00.123456789Z")
	}
	if entry.Log != "Hello world\n" {
		t.Errorf("Log = %q, want %q", entry.Log, "Hello world\n")
	}
	if entry.Stream != "stdout" {
		t.Errorf("Stream = %q, want %q", entry.Stream, "stdout")
	}
}

func TestParseLogLine_WithoutTimestamp(t *testing.T) {
	line := "Just a simple log line"

	entry := parseLogLine(line)

	if entry.Time != "" {
		t.Errorf("Time = %q, want empty", entry.Time)
	}
	if entry.Log != "Just a simple log line\n" {
		t.Errorf("Log = %q, want %q", entry.Log, "Just a simple log line\n")
	}
}

func TestParseLogLine_EmptyLine(t *testing.T) {
	entry := parseLogLine("")

	if entry.Log != "\n" {
		t.Errorf("Log = %q, want %q", entry.Log, "\n")
	}
}

func TestParseLogLine_ShortLine(t *testing.T) {
	entry := parseLogLine("short")

	if entry.Time != "" {
		t.Errorf("Time = %q, want empty for short line", entry.Time)
	}
	if entry.Log != "short\n" {
		t.Errorf("Log = %q, want %q", entry.Log, "short\n")
	}
}

func TestParseLogLine_TimestampWithMultipleSpaces(t *testing.T) {
	line := "2024-01-15T10:30:00.123456789Z multi  word  log"

	entry := parseLogLine(line)

	if entry.Time != "2024-01-15T10:30:00.123456789Z" {
		t.Errorf("Time = %q", entry.Time)
	}
	if entry.Log != "multi  word  log\n" {
		t.Errorf("Log = %q", entry.Log)
	}
}
