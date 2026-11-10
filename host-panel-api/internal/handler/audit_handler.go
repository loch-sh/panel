package handler

import (
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"host-panel-api/internal/audit"
	"host-panel-api/internal/middleware"
)

// ScopeAuditRead is the API key scope of the audit trail.
const ScopeAuditRead = "audit:read"

// Bounds of the query parameters.
const (
	maxAuditText     = 256 // user and q
	maxAuditValues   = 100 // event, exclude, level occurrences
	maxAuditEventLen = 100
	maxAuditLogValue = 512 // filters echoed in the "audit trail viewed" entry
)

// AuditHandler exposes the audit trail, read-only and to admins only: there
// is no route to write, edit or delete entries.
type AuditHandler struct {
	Reader *audit.Reader
	Audit  *slog.Logger
	Logger *slog.Logger
}

// List handles GET /audit?from=&to=&user=&event=&exclude=&level=&q=&before=&limit=.
// from/to are RFC 3339 dates (to exclusive, "+" of an offset URL-encoded);
// event, exclude and level may repeat.
func (h *AuditHandler) List(w http.ResponseWriter, r *http.Request) {
	if !middleware.RequireScope(r.Context(), ScopeAuditRead) {
		writeError(w, http.StatusForbidden, ErrInsufficientScope, "Missing scope "+ScopeAuditRead)
		return
	}
	f, err := parseAuditFilter(r.URL.Query())
	if err != nil {
		writeError(w, http.StatusBadRequest, ErrInvalidFilter, err.Error())
		return
	}
	page, err := h.Reader.Query(f)
	if err != nil {
		h.Logger.Error("audit: reading the audit log", "error", err)
		writeError(w, http.StatusInternalServerError, ErrAuditReadFailed, "Cannot read the audit log")
		return
	}
	h.auditViewed(r)
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, page)
}

// auditViewed records the consultation, every page included, with who read
// (session user or API key).
func (h *AuditHandler) auditViewed(r *http.Request) {
	filters := r.URL.RawQuery
	if len(filters) > maxAuditLogValue {
		filters = filters[:maxAuditLogValue]
	}
	attrs := []any{"filters", filters, "ip", middleware.ClientIP(r)}
	if user, ok := middleware.UserFromCtx(r.Context()); ok {
		attrs = append(attrs, "user", user.Email)
	}
	if key, ok := middleware.APIKeyFromCtx(r.Context()); ok {
		attrs = append(attrs, "api_key", key.ID)
	}
	h.Audit.Info("audit trail viewed", attrs...)
}

func parseAuditFilter(q url.Values) (audit.Filter, error) {
	f := audit.Filter{User: q.Get("user"), Query: q.Get("q")}
	if len(f.User) > maxAuditText || len(f.Query) > maxAuditText {
		return f, errors.New("user and q are limited to " + strconv.Itoa(maxAuditText) + " characters")
	}
	var err error
	if f.From, err = parseAuditTime(q.Get("from")); err != nil {
		return f, errors.New("invalid from date (RFC 3339 expected)")
	}
	if f.To, err = parseAuditTime(q.Get("to")); err != nil {
		return f, errors.New("invalid to date (RFC 3339 expected)")
	}
	if f.Events, err = parseAuditEvents(q["event"]); err != nil {
		return f, err
	}
	if f.ExcludeEvents, err = parseAuditEvents(q["exclude"]); err != nil {
		return f, err
	}
	if f.Levels, err = parseAuditLevels(q["level"]); err != nil {
		return f, err
	}
	if v := q.Get("before"); v != "" {
		if f.Before, err = strconv.ParseInt(v, 10, 64); err != nil || f.Before <= 0 {
			return f, errors.New("invalid cursor")
		}
	}
	if v := q.Get("limit"); v != "" {
		if f.Limit, err = strconv.Atoi(v); err != nil || f.Limit < 1 || f.Limit > audit.MaxLimit {
			return f, errors.New("limit must be between 1 and " + strconv.Itoa(audit.MaxLimit))
		}
	}
	return f, nil
}

func parseAuditTime(v string) (time.Time, error) {
	if v == "" {
		return time.Time{}, nil
	}
	return time.Parse(time.RFC3339, v)
}

func parseAuditEvents(values []string) (map[string]bool, error) {
	if len(values) == 0 {
		return nil, nil
	}
	if len(values) > maxAuditValues {
		return nil, errors.New("too many events")
	}
	set := make(map[string]bool, len(values))
	for _, v := range values {
		if v == "" || len(v) > maxAuditEventLen {
			return nil, errors.New("event names must have 1 to " + strconv.Itoa(maxAuditEventLen) + " characters")
		}
		set[v] = true
	}
	return set, nil
}

var auditLevels = map[string]bool{"INFO": true, "WARN": true, "ERROR": true}

func parseAuditLevels(values []string) (map[string]bool, error) {
	if len(values) == 0 {
		return nil, nil
	}
	if len(values) > maxAuditValues {
		return nil, errors.New("too many levels")
	}
	set := make(map[string]bool, len(values))
	for _, v := range values {
		v = strings.ToUpper(v)
		if !auditLevels[v] {
			return nil, errors.New("level must be INFO, WARN or ERROR")
		}
		set[v] = true
	}
	return set, nil
}
