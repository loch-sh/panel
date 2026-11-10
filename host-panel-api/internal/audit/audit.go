package audit

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// New creates an audit logger that writes to {dir}/audit.log.
// The directory is created if it does not exist.
// Returns the logger and the underlying file (caller must close it on shutdown).
func New(dir string) (*slog.Logger, *os.File, error) {
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return nil, nil, fmt.Errorf("resolve audit log directory: %w", err)
	}

	if err := os.MkdirAll(absDir, 0o750); err != nil {
		return nil, nil, fmt.Errorf("create audit log directory: %w", err)
	}

	path := filepath.Join(absDir, "audit.log")
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o640)
	if err != nil {
		return nil, nil, fmt.Errorf("open audit log file: %w", err)
	}

	logger := slog.New(slog.NewJSONHandler(&failureReporter{w: file}, &slog.HandlerOptions{
		Level:       slog.LevelInfo,
		ReplaceAttr: boundAttr,
	}))

	return logger, file, nil
}

// maxAttrLen bounds every string attribute: an entry stays far below the
// line size the reader accepts, so that no entry can be hidden from the
// audit trail by an oversized value (a header, a path, a name).
const maxAttrLen = 2048

func boundAttr(_ []string, a slog.Attr) slog.Attr {
	if a.Value.Kind() == slog.KindString {
		if v := a.Value.String(); len(v) > maxAttrLen {
			a.Value = slog.StringValue(strings.ToValidUTF8(v[:maxAttrLen], "") + "…")
		}
	}
	return a
}

// failureReporter reports failed writes (disk full, file removed) on
// stderr: slog drops write errors, an audit entry must not be lost silently.
type failureReporter struct {
	w      io.Writer
	mu     sync.Mutex
	failed int
}

func (f *failureReporter) Write(p []byte) (int, error) {
	n, err := f.w.Write(p)
	if err != nil {
		f.mu.Lock()
		f.failed++
		count := f.failed
		f.mu.Unlock()
		fmt.Fprintf(os.Stderr, "audit: write failed (%d entries lost so far): %v\n", count, err)
	}
	return n, err
}
