package handler

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"host-panel-api/internal/audit"
	"host-panel-api/internal/middleware"
	"host-panel-api/internal/model"
)

func newAuditHandler(t *testing.T, content string) (*AuditHandler, *bytes.Buffer) {
	t.Helper()
	p := filepath.Join(t.TempDir(), audit.FileName)
	if err := os.WriteFile(p, []byte(content), 0o640); err != nil {
		t.Fatal(err)
	}
	var trail bytes.Buffer
	return &AuditHandler{
		Reader: &audit.Reader{Path: p},
		Audit:  slog.New(slog.NewJSONHandler(&trail, nil)),
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}, &trail
}

func callAudit(t *testing.T, h *AuditHandler, query string, key *model.APIKey) (*httptest.ResponseRecorder, audit.Page) {
	t.Helper()
	req := httptest.NewRequest("GET", "/audit?"+query, nil)
	ctx := middleware.WithUser(req.Context(), &model.User{Email: "admin@x.fr", Profile: model.ProfileAdmin})
	if key != nil {
		ctx = middleware.WithAPIKey(ctx, key)
	}
	rec := httptest.NewRecorder()
	h.List(rec, req.WithContext(ctx))
	var page audit.Page
	if rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
			t.Fatal(err)
		}
	}
	return rec, page
}

const auditFixture = `{"time":"2026-11-01T10:00:00Z","level":"INFO","msg":"cron job created","user":"a@x.fr","id":"c1"}` + "\n" +
	`{"time":"2026-11-02T10:00:00Z","level":"WARN","msg":"access denied","user":"b@x.fr"}` + "\n"

func TestAuditList(t *testing.T) {
	h, trail := newAuditHandler(t, auditFixture)

	rec, page := callAudit(t, h, "", nil)
	if rec.Code != http.StatusOK || len(page.Entries) != 2 || page.Entries[0].Event != "access denied" {
		t.Fatalf("%d %+v", rec.Code, page)
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("the trail must not be cached")
	}
	if !strings.Contains(trail.String(), `"msg":"audit trail viewed"`) || !strings.Contains(trail.String(), `"user":"admin@x.fr"`) {
		t.Fatalf("consultation not audited: %s", trail.String())
	}
	if _, page := callAudit(t, h, "from=2026-11-01T00:00:00Z&to=2026-11-02T00:00:00Z&level=info", nil); len(page.Entries) != 1 || page.Entries[0].Fields["id"] != "c1" {
		t.Fatalf("filters: %+v", page)
	}
	if _, page := callAudit(t, h, "event=cron+job+created&event=user+logged+in", nil); len(page.Entries) != 1 {
		t.Fatalf("events: %+v", page)
	}
	if _, page := callAudit(t, h, "exclude=access+denied", nil); len(page.Entries) != 1 {
		t.Fatalf("exclude: %+v", page)
	}
	// Pagination through next.
	_, first := callAudit(t, h, "limit=1", nil)
	if first.Next == 0 {
		t.Fatal("next cursor expected")
	}
	if _, second := callAudit(t, h, "limit=1&before="+itoa(first.Next), nil); len(second.Entries) != 1 || second.Entries[0].Event != "cron job created" || second.Next != 0 {
		t.Fatalf("second page: %+v", second)
	}
	long := strings.Repeat("x", maxAuditText+1)
	for _, q := range []string{"from=yesterday", "level=DEBUG", "before=-1", "limit=10000", "user=" + long, "q=" + long, "event="} {
		if rec, _ := callAudit(t, h, q, nil); rec.Code != http.StatusBadRequest {
			t.Fatalf("%.40s: %d", q, rec.Code)
		}
	}
}

func TestAuditListScopes(t *testing.T) {
	h, trail := newAuditHandler(t, auditFixture)
	if rec, _ := callAudit(t, h, "", &model.APIKey{ID: "k1", Scopes: []string{"containers:read"}}); rec.Code != http.StatusForbidden {
		t.Fatalf("scope: %d", rec.Code)
	}
	if rec, _ := callAudit(t, h, "", &model.APIKey{ID: "k2", Scopes: []string{ScopeAuditRead}}); rec.Code != http.StatusOK {
		t.Fatalf("audit:read: %d", rec.Code)
	}
	if !strings.Contains(trail.String(), `"api_key":"k2"`) {
		t.Fatalf("the key that read must be recorded: %s", trail.String())
	}
}

func TestAuditListReadError(t *testing.T) {
	h, _ := newAuditHandler(t, "")
	h.Reader.Path = t.TempDir() // a directory: cannot be read as a file
	if rec, _ := callAudit(t, h, "", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("%d", rec.Code)
	}
}

func itoa(n int64) string {
	b, _ := json.Marshal(n)
	return string(b)
}
