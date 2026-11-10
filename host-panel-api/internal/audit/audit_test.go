package audit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestNew_CreatesLogFile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "audit")

	logger, file, err := New(dir)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	defer file.Close()

	if logger == nil {
		t.Fatal("logger should not be nil")
	}

	path := filepath.Join(dir, "audit.log")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Error("audit.log file should exist")
	}
}

func TestNew_CreatesDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "audit", "dir")

	_, file, err := New(dir)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	defer file.Close()

	if _, err := os.Stat(dir); os.IsNotExist(err) {
		t.Error("directory should have been created")
	}
}

func TestNew_WritesJSONLines(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "audit")

	logger, file, err := New(dir)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	logger.Info("test event", "action", "create", "user", "admin@test.com")
	file.Close()

	path := filepath.Join(dir, "audit.log")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	content := string(data)
	if len(content) == 0 {
		t.Fatal("audit.log should not be empty after writing")
	}

	// slog.JSONHandler writes JSON lines
	if content[0] != '{' {
		t.Errorf("expected JSON output, got: %s", content[:50])
	}
}

func TestNew_AppendMode(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "audit")

	// First write
	logger1, file1, err := New(dir)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	logger1.Info("first")
	file1.Close()

	// Second write (should append, not truncate)
	logger2, file2, err := New(dir)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	logger2.Info("second")
	file2.Close()

	path := filepath.Join(dir, "audit.log")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	content := string(data)
	// Should contain both entries (two JSON lines)
	lines := 0
	for _, c := range content {
		if c == '\n' {
			lines++
		}
	}
	if lines < 2 {
		t.Errorf("expected at least 2 lines (append mode), got %d", lines)
	}
}

func TestNew_BoundsAttributes(t *testing.T) {
	dir := t.TempDir()
	logger, file, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	logger.Info("big", "path", strings.Repeat("é", 10000))
	data, _ := os.ReadFile(filepath.Join(dir, FileName))
	if len(data) > maxAttrLen+200 {
		t.Fatalf("entry of %d bytes: attributes must be bounded", len(data))
	}
	if !utf8.Valid(data) {
		t.Fatal("truncation must not split a character")
	}
}
