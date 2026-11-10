package audit

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeLog(t *testing.T, lines ...string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), FileName)
	if err := os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	return p
}

func line(i int, event, user string) string {
	ts := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC).Add(time.Duration(i) * time.Hour).Format(time.RFC3339Nano)
	return fmt.Sprintf(`{"time":%q,"level":"INFO","msg":%q,"user":%q,"n":%d}`, ts, event, user, i)
}

func TestQueryNewestFirstWithCursor(t *testing.T) {
	var lines []string
	for i := range 2500 { // several 64 KB chunks
		lines = append(lines, line(i, "container action", "a@x.fr"))
	}
	r := &Reader{Path: writeLog(t, lines...)}
	seen := map[float64]bool{}
	var cursor int64
	last := 1e9
	for pages := 0; ; pages++ {
		p, err := r.Query(Filter{Limit: 500, Before: cursor})
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range p.Entries {
			n := e.Fields["n"].(float64)
			if seen[n] || n >= last {
				t.Fatalf("entry %v duplicated or out of order", n)
			}
			seen[n], last = true, n
		}
		if p.Next == 0 {
			break
		}
		cursor = p.Next
		if pages > 5 {
			t.Fatal("pagination does not end")
		}
	}
	if len(seen) != 2500 {
		t.Fatalf("%d entries, want 2500", len(seen))
	}
}

func TestQueryFilters(t *testing.T) {
	r := &Reader{Path: writeLog(t,
		line(0, "user logged in", "alice@x.fr"),
		line(1, "container action", "bob@x.fr"),
		`{"time":"bad`, // partial line: skipped
		line(2, "cron job created", "Alice@x.fr"),
		line(3, "container action", "alice@x.fr"),
	)}
	from := time.Date(2026, 11, 1, 1, 0, 0, 0, time.UTC)
	to := time.Date(2026, 11, 1, 3, 0, 0, 0, time.UTC)
	cases := []struct {
		name string
		f    Filter
		want []float64
	}{
		{"all", Filter{}, []float64{3, 2, 1, 0}},
		{"user", Filter{User: "ALICE"}, []float64{3, 2, 0}},
		{"event", Filter{Events: map[string]bool{"container action": true}}, []float64{3, 1}},
		{"dates", Filter{From: from, To: to}, []float64{2, 1}},
		{"text", Filter{Query: "CRON"}, []float64{2}},
		{"level", Filter{Levels: map[string]bool{"WARN": true}}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p, err := r.Query(c.f)
			if err != nil {
				t.Fatal(err)
			}
			var got []float64
			for _, e := range p.Entries {
				got = append(got, e.Fields["n"].(float64))
			}
			if fmt.Sprint(got) != fmt.Sprint(c.want) {
				t.Fatalf("got %v, want %v", got, c.want)
			}
		})
	}
}

func TestQueryMissingFile(t *testing.T) {
	p, err := (&Reader{Path: filepath.Join(t.TempDir(), "none.log")}).Query(Filter{})
	if err != nil || len(p.Entries) != 0 {
		t.Fatalf("%v %v", p, err)
	}
}

func readAll(t *testing.T, r *Reader, f Filter) []float64 {
	t.Helper()
	var got []float64
	for pages := 0; ; pages++ {
		p, err := r.Query(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range p.Entries {
			got = append(got, e.Fields["n"].(float64))
		}
		if p.Next == 0 {
			return got
		}
		if p.Next >= f.Before && f.Before != 0 {
			t.Fatalf("cursor does not move back: %d -> %d", f.Before, p.Next)
		}
		f.Before = p.Next
		if pages > 10000 {
			t.Fatal("pagination does not end")
		}
	}
}

func TestQueryEdgeCases(t *testing.T) {
	long := `{"time":"2026-11-01T00:30:00Z","level":"INFO","msg":"big","n":-1,"x":"` + strings.Repeat("y", maxLineSize+10) + `"}`
	p := filepath.Join(t.TempDir(), FileName)
	content := strings.Join([]string{
		line(0, "a", "u"),
		long, // overlong: skipped, its neighbours are read
		`{"level":"INFO","msg":"no time","n":-2}`, // no time: skipped, does not stop a From scan
		line(1, "a", "u"),
		line(2, "a", "u"), // last line without newline
	}, "\n")
	if err := os.WriteFile(p, []byte(content), 0o640); err != nil {
		t.Fatal(err)
	}
	r := &Reader{Path: p}
	if got := readAll(t, r, Filter{}); fmt.Sprint(got) != "[2 1 0]" {
		t.Fatalf("got %v", got)
	}
	from := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	if got := readAll(t, r, Filter{From: from}); fmt.Sprint(got) != "[2 1 0]" {
		t.Fatalf("a line without time must not stop the scan: %v", got)
	}
	// Limit equal to the number of entries: no next page.
	if pg, _ := r.Query(Filter{Limit: 3}); pg.Next != 0 || len(pg.Entries) != 3 {
		t.Fatalf("%+v", pg)
	}
	// IDs are unique offsets, stable across queries.
	a, _ := r.Query(Filter{})
	b, _ := r.Query(Filter{Limit: 1})
	if a.Entries[0].ID != b.Entries[0].ID || a.Entries[0].ID == a.Entries[1].ID {
		t.Fatalf("ids: %v %v", a.Entries, b.Entries)
	}
	// A cursor beyond the file is the end of the file.
	if got := readAll(t, r, Filter{Before: 1 << 40}); len(got) != 3 {
		t.Fatalf("got %v", got)
	}
}

func TestQueryScanLimitResumes(t *testing.T) {
	var lines []string
	for i := range 3000 {
		lines = append(lines, line(i, "x", "u"))
	}
	// Only the first entry matches: each query hits the scan limit.
	lines[0] = line(0, "wanted", "u")
	r := &Reader{Path: writeLog(t, lines...), MaxScanBytes: chunkSize}
	p, err := r.Query(Filter{Events: map[string]bool{"wanted": true}})
	if err != nil || len(p.Entries) != 0 || p.Next == 0 {
		t.Fatalf("an unfinished scan returns a cursor: %+v %v", p, err)
	}
	if got := readAll(t, r, Filter{Events: map[string]bool{"wanted": true}}); fmt.Sprint(got) != "[0]" {
		t.Fatalf("got %v", got)
	}
	// Without filter, resuming finds every entry once.
	got := readAll(t, r, Filter{Limit: 500})
	if len(got) != 3000 {
		t.Fatalf("%d entries", len(got))
	}
	for i, n := range got {
		if n != float64(2999-i) {
			t.Fatalf("entry %d = %v", i, n)
		}
	}
}

func TestQueryExcludeAndCombined(t *testing.T) {
	r := &Reader{Path: writeLog(t,
		line(0, "audit trail viewed", "a@x.fr"),
		line(1, "container action", "a@x.fr"),
		line(2, "container action", "b@x.fr"),
	)}
	got := readAll(t, r, Filter{ExcludeEvents: map[string]bool{"audit trail viewed": true}, User: "a@", To: time.Date(2026, 11, 1, 5, 0, 0, 0, time.UTC)})
	if fmt.Sprint(got) != "[1]" {
		t.Fatalf("got %v", got)
	}
}
