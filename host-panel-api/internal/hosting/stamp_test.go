package hosting

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestStampCompose(t *testing.T) {
	at := time.Date(2026, 11, 4, 10, 12, 0, 0, time.UTC)
	in := []byte("# header 1\n# header 2\nname: x\n")
	once := stampCompose(in, "admin@example.com", OpUpdate, at)
	want := "# header 1\n# header 2\n# Last deployed by admin@example.com at 2026-11-04T10:12:00Z (update)\nname: x\n"
	if string(once) != want {
		t.Fatalf("stamp:\n%s", once)
	}
	twice := stampCompose(once, "other\n@evil", OpDeploy, at.Add(time.Hour))
	if strings.Count(string(twice), deployStampPrefix) != 1 || !strings.Contains(string(twice), "by other@evil at 2026-11-04T11:12:00Z (deploy)") {
		t.Fatalf("restamp:\n%s", twice)
	}
	if string(stripStamp(twice)) != string(in) {
		t.Fatalf("strip:\n%s", stripStamp(twice))
	}
}

func TestStackIsStampedOnEachDeployment(t *testing.T) {
	m, cfg := newTestManager(t, &fakeRunner{}, &fakeDocker{})
	ctx := context.Background()
	if _, err := m.Create(ctx, validSpec("site"), Snippets{}, "alice@example.com"); err != nil {
		t.Fatal(err)
	}
	waitOp(t, m, "site")
	path := filepath.Join(cfg.StacksDir, "hosted-site", ComposeFile)
	if data, _ := os.ReadFile(path); !strings.Contains(string(data), "# Last deployed by alice@example.com at ") || !strings.Contains(string(data), "(create)") {
		t.Fatalf("not stamped on create:\n%s", data)
	}
	if d, _ := m.Get(ctx, "site"); d.Modified {
		t.Fatal("the stamp must not count as a manual edit")
	}

	if _, err := m.Deploy(ctx, "site", false, false, "bob@example.com"); err != nil {
		t.Fatal(err)
	}
	waitOp(t, m, "site")
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "by bob@example.com") || strings.Contains(string(data), "alice") {
		t.Fatalf("not restamped on deploy:\n%s", data)
	}
	if d, _ := m.Get(ctx, "site"); d.Modified {
		t.Fatal("a redeploy must not mark the stack modified")
	}

	// A real manual edit is still detected.
	writeTestFile(t, path, append(data, []byte("# edited\n")...), 0o640)
	if d, _ := m.Get(ctx, "site"); !d.Modified {
		t.Fatal("manual edit not detected")
	}
}

func TestSanitizeActorUnicodeBreaks(t *testing.T) {
	got := sanitizeActor("x x-evil: 1 \u202e" + strings.Repeat("a", 300))
	if strings.ContainsAny(got, "  \u202e\n") || len([]rune(got)) > maxActorRunes {
		t.Fatalf("actor = %q", got)
	}
}
