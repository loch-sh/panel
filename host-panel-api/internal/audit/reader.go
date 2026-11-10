package audit

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// FileName is the audit log file inside the log directory.
const FileName = "audit.log"

// Limits of a query: entries per page, length of a line (longer ones are
// skipped), bytes read by one query (the caller continues with the cursor).
const (
	DefaultLimit        = 100
	MaxLimit            = 500
	DefaultMaxScanBytes = 64 << 20
	maxLineSize         = 64 << 10
	chunkSize           = 64 << 10
)

// FilePath returns the audit log path of a log directory.
func FilePath(dir string) (string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	return filepath.Join(abs, FileName), nil
}

// Entry is one audit record. ID is the offset of the record in the file:
// stable and unique while the file is not rotated. Fields holds the
// attributes besides the standard ones.
type Entry struct {
	ID     int64          `json:"id"`
	Time   time.Time      `json:"time"`
	Level  string         `json:"level"`
	Event  string         `json:"event"`
	User   string         `json:"user,omitempty"`
	Fields map[string]any `json:"fields,omitempty"`
}

// Filter selects entries. Zero values match everything.
type Filter struct {
	From, To      time.Time       // To is exclusive
	User          string          // case-insensitive substring of the user
	Events        map[string]bool // exact event names
	ExcludeEvents map[string]bool // events left out
	Levels        map[string]bool // INFO, WARN, ERROR
	Query         string          // case-insensitive substring of the whole record
	// Before is the cursor: only entries before this offset (0 = end of
	// file). It refers to the current file: a rotation invalidates it.
	Before int64
	Limit  int
}

// Page is a page of entries, newest first. Next is the cursor of the
// following (older) page, 0 when the start of the file was reached.
type Page struct {
	Entries []Entry `json:"entries"`
	Next    int64   `json:"next,omitempty"`
}

// Reader reads an audit log file. It never writes to it: the audit trail
// can only be consulted.
type Reader struct {
	Path         string
	MaxScanBytes int64 // bytes read by one query (0 = DefaultMaxScanBytes)
}

// verdict is what the scan does with a line.
type verdict int

const (
	skipLine verdict = iota // not an entry, or not matching
	keptLine                // added to the page
	pageFull                // matching, but the page is full: next cursor
	tooOld                  // older than From: nothing older can match
)

// Query returns the entries matching f, newest first. The file is read
// backwards from the cursor and the scan stops at the first entry older
// than f.From: the file is in chronological order (concurrent writes may
// be out of order by a few milliseconds, which only matters at the bound).
func (r *Reader) Query(f Filter) (Page, error) {
	if f.Limit <= 0 {
		f.Limit = DefaultLimit
	}
	f.Limit = min(f.Limit, MaxLimit)
	f.User = strings.ToLower(f.User)
	f.Query = strings.ToLower(f.Query)

	file, err := os.Open(r.Path)
	if errors.Is(err, os.ErrNotExist) {
		return Page{Entries: []Entry{}}, nil
	}
	if err != nil {
		return Page{}, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return Page{}, err
	}
	end := info.Size()
	if f.Before > 0 && f.Before < end {
		end = f.Before
	}
	maxScan := r.MaxScanBytes
	if maxScan <= 0 {
		maxScan = DefaultMaxScanBytes
	}
	return scanBackwards(file, end, maxScan, &f)
}

// scanBackwards reads [0, end) of file from the end, chunk by chunk.
func scanBackwards(file io.ReaderAt, end, maxScan int64, f *Filter) (Page, error) {
	page := Page{Entries: []Entry{}}
	// tail is the end of a line whose beginning is not read yet (it never
	// holds a newline); it starts at offset pos.
	var tail []byte
	pos, scanned := end, int64(0)
	for pos > 0 {
		if scanned >= maxScan {
			// Not finished: continue from the line being read.
			page.Next = pos + int64(len(tail))
			return page, nil
		}
		n := min(int64(chunkSize), pos)
		pos -= n
		buf := make([]byte, n, n+int64(len(tail)))
		if _, err := file.ReadAt(buf, pos); err != nil && !errors.Is(err, io.EOF) {
			return Page{}, err
		}
		scanned += n
		buf = append(buf, tail...)
		// Every line after a newline is complete.
		for {
			i := bytes.LastIndexByte(buf, '\n')
			if i < 0 {
				break
			}
			switch consider(buf[i+1:], pos+int64(i)+1, f, &page) {
			case pageFull, tooOld:
				return page, nil
			}
			buf = buf[:i]
		}
		tail = buf
		if len(tail) > maxLineSize {
			// Overlong line: dropped. Its beginning, read with the next
			// chunk, is not valid JSON and is skipped too.
			tail = nil
		}
	}
	if len(tail) > 0 {
		consider(tail, 0, f, &page)
	}
	return page, nil
}

// consider decides what to do with the line starting at offset.
func consider(line []byte, offset int64, f *Filter, page *Page) verdict {
	next := offset + int64(len(line)) + 1 // start of the following line
	line = bytes.TrimSpace(line)
	if len(line) == 0 || len(line) > maxLineSize {
		return skipLine
	}
	var raw map[string]any
	if json.Unmarshal(line, &raw) != nil {
		return skipLine // partial line being written, or not JSON
	}
	e, ok := toEntry(raw)
	if !ok {
		return skipLine // no valid time: neither matched nor used to stop
	}
	e.ID = offset
	if !f.From.IsZero() && e.Time.Before(f.From) {
		return tooOld
	}
	if !matches(e, line, f) {
		return skipLine
	}
	if len(page.Entries) == f.Limit {
		page.Next = next // this entry is the first of the next page
		return pageFull
	}
	page.Entries = append(page.Entries, e)
	return keptLine
}

func matches(e Entry, line []byte, f *Filter) bool {
	switch {
	case !f.To.IsZero() && !e.Time.Before(f.To):
		return false
	case len(f.Events) > 0 && !f.Events[e.Event]:
		return false
	case f.ExcludeEvents[e.Event]:
		return false
	case len(f.Levels) > 0 && !f.Levels[e.Level]:
		return false
	case f.User != "" && !strings.Contains(strings.ToLower(e.User), f.User):
		return false
	case f.Query != "" && !bytes.Contains(bytes.ToLower(line), []byte(f.Query)):
		return false
	}
	return true
}

// toEntry splits a record into its standard attributes and the others.
// ok is false without a valid time.
func toEntry(raw map[string]any) (e Entry, ok bool) {
	s, _ := raw["time"].(string)
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return Entry{}, false
	}
	e.Time = t
	e.Level, _ = raw["level"].(string)
	e.Event, _ = raw["msg"].(string)
	e.User, _ = raw["user"].(string)
	for _, k := range []string{"time", "level", "msg", "user"} {
		delete(raw, k)
	}
	if len(raw) > 0 {
		e.Fields = raw
	}
	return e, true
}
