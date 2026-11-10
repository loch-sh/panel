package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"host-panel-api/internal/hosting"
	"host-panel-api/internal/middleware"
	"host-panel-api/internal/model"
)

// mockHostingManager records calls and returns canned results.
type mockHostingManager struct {
	err        error
	lastSpec   *hosting.Spec
	lastSnip   hosting.Snippets
	lastForce  bool
	lastPull   bool
	lastPurge  bool
	lastActor  string
	checkError error
}

var testOp = &hosting.Operation{ID: "op1", Kind: hosting.OpCreate, State: hosting.OpRunning}

func (m *mockHostingManager) List(context.Context) ([]hosting.StackSummary, error) {
	return []hosting.StackSummary{{Name: "site"}}, m.err
}
func (m *mockHostingManager) Get(_ context.Context, name string) (*hosting.StackDetail, error) {
	if m.err != nil {
		return nil, m.err
	}
	return &hosting.StackDetail{
		StackSummary: hosting.StackSummary{Name: name},
		Spec:         &hosting.Spec{Name: name, Env: []hosting.EnvVar{{Name: "DB_PASSWORD", Value: "s3cret"}}},
		Snippets:     hosting.Snippets{Vhost: "auth_basic_user_file secret;"},
		Compose:      "environment:\n  DB_PASSWORD: s3cret\n",
	}, nil
}
func (m *mockHostingManager) Sites() ([]hosting.SiteInfo, error) { return nil, m.err }
func (m *mockHostingManager) PHPModules(context.Context, hosting.Template, string) ([]hosting.PHPModule, error) {
	return []hosting.PHPModule{{Name: "ioncube", File: "00-ioncube.ini"}}, m.err
}
func (m *mockHostingManager) CheckName(context.Context, string) error {
	return m.checkError
}
func (m *mockHostingManager) Create(_ context.Context, spec *hosting.Spec, sn hosting.Snippets, actor string) (*hosting.Operation, error) {
	m.lastSpec, m.lastSnip, m.lastActor = spec, sn, actor
	return testOp, m.err
}
func (m *mockHostingManager) Update(_ context.Context, _ string, spec *hosting.Spec, sn hosting.Snippets, force bool, actor string) (*hosting.Operation, error) {
	m.lastSpec, m.lastSnip, m.lastForce, m.lastActor = spec, sn, force, actor
	return testOp, m.err
}
func (m *mockHostingManager) Deploy(_ context.Context, _ string, pull, force bool, actor string) (*hosting.Operation, error) {
	m.lastPull, m.lastForce, m.lastActor = pull, force, actor
	return testOp, m.err
}
func (m *mockHostingManager) Delete(_ context.Context, _ string, purge bool, actor string) (*hosting.Operation, error) {
	m.lastPurge, m.lastActor = purge, actor
	return testOp, m.err
}

func newHostingTestHandler(enabled bool) (*HostingHandler, *mockHostingManager) {
	mgr := &mockHostingManager{}
	return &HostingHandler{
		Manager: mgr,
		Enabled: enabled,
		Logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
	}, mgr
}

func hostingRequest(method, target, body string, scopes ...string) *http.Request {
	req := httptest.NewRequest(method, target, bytes.NewBufferString(body))
	ctx := middleware.WithUser(req.Context(), &model.User{Email: "admin@example.com", Profile: model.ProfileAdmin})
	if scopes != nil {
		ctx = middleware.WithAPIKey(ctx, &model.APIKey{Scopes: scopes})
	}
	return req.WithContext(ctx)
}

func serveHosting(h *HostingHandler, req *http.Request) *httptest.ResponseRecorder {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /hosting/config", h.Config)
	mux.HandleFunc("GET /hosting/stacks", h.List)
	mux.HandleFunc("POST /hosting/stacks", h.Create)
	mux.HandleFunc("GET /hosting/stacks/check-name", h.CheckName)
	mux.HandleFunc("GET /hosting/stacks/{name}", h.Get)
	mux.HandleFunc("PUT /hosting/stacks/{name}", h.Update)
	mux.HandleFunc("DELETE /hosting/stacks/{name}", h.Delete)
	mux.HandleFunc("POST /hosting/stacks/{name}/deploy", h.Deploy)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func decodeBody(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid JSON body %q: %v", rec.Body.String(), err)
	}
	return body
}

func TestHostingConfigDisabled(t *testing.T) {
	h, _ := newHostingTestHandler(false)
	rec := serveHosting(h, hostingRequest("GET", "/hosting/config", ""))
	body := decodeBody(t, rec)
	if rec.Code != http.StatusOK || body["enabled"] != false || body["templates"] != nil {
		t.Fatalf("got %d %v", rec.Code, body)
	}
}

func TestHostingDisabledRejectsRoutes(t *testing.T) {
	h, _ := newHostingTestHandler(false)
	rec := serveHosting(h, hostingRequest("GET", "/hosting/stacks", ""))
	if rec.Code != http.StatusNotFound || decodeBody(t, rec)["code"] != ErrHostingDisabled {
		t.Fatalf("got %d %s", rec.Code, rec.Body)
	}
}

func TestHostingScopes(t *testing.T) {
	h, _ := newHostingTestHandler(true)
	tests := []struct {
		method, target, body string
		scopes               []string
		want                 int
	}{
		{"GET", "/hosting/stacks", "", []string{"hosting:read"}, http.StatusOK},
		{"GET", "/hosting/stacks", "", []string{"containers:read"}, http.StatusForbidden},
		{"POST", "/hosting/stacks", `{"name":"site"}`, []string{"hosting:read"}, http.StatusForbidden},
		{"POST", "/hosting/stacks", `{"name":"site"}`, []string{"hosting:write"}, http.StatusAccepted},
		{"DELETE", "/hosting/stacks/site", "", []string{"hosting:read"}, http.StatusForbidden},
		{"POST", "/hosting/stacks/site/deploy", "", []string{"*"}, http.StatusAccepted},
	}
	for _, tt := range tests {
		rec := serveHosting(h, hostingRequest(tt.method, tt.target, tt.body, tt.scopes...))
		if rec.Code != tt.want {
			t.Errorf("%s %s with %v: got %d, want %d (%s)", tt.method, tt.target, tt.scopes, rec.Code, tt.want, rec.Body)
		}
	}
}

func TestHostingCreateDecodesFlatBody(t *testing.T) {
	h, mgr := newHostingTestHandler(true)
	body := `{"name":"site","template":"php-nginx","phpVersion":"8.4","domains":["a.com"],
		"https":true,"resources":{"memoryMB":512,"cpus":1},"env":[{"name":"A","value":"b"}],
		"phpIni":"x=1","vhost":"# v"}`
	rec := serveHosting(h, hostingRequest("POST", "/hosting/stacks", body))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("got %d %s", rec.Code, rec.Body)
	}
	if mgr.lastSpec.Name != "site" || mgr.lastSpec.Template != hosting.TemplatePHPNginx ||
		mgr.lastSnip.PHPIni != "x=1" || mgr.lastSnip.Vhost != "# v" ||
		len(mgr.lastSpec.Env) != 1 || mgr.lastActor != "admin@example.com" {
		t.Fatalf("decoded spec %+v snippets %+v actor %q", mgr.lastSpec, mgr.lastSnip, mgr.lastActor)
	}
	resp := decodeBody(t, rec)
	if resp["name"] != "site" || resp["operation"].(map[string]any)["state"] != "running" {
		t.Fatalf("response %v", resp)
	}
}

func TestHostingCreateRejectsUnknownFields(t *testing.T) {
	h, _ := newHostingTestHandler(true)
	rec := serveHosting(h, hostingRequest("POST", "/hosting/stacks", `{"name":"site","privileged":true}`))
	if rec.Code != http.StatusBadRequest || decodeBody(t, rec)["code"] != ErrInvalidRequestBody {
		t.Fatalf("got %d %s", rec.Code, rec.Body)
	}
}

func TestHostingQueryFlags(t *testing.T) {
	h, mgr := newHostingTestHandler(true)
	serveHosting(h, hostingRequest("PUT", "/hosting/stacks/site?force=true", `{}`))
	if !mgr.lastForce {
		t.Fatal("update force not passed")
	}
	serveHosting(h, hostingRequest("POST", "/hosting/stacks/site/deploy?pull=true&force=true", ""))
	serveHosting(h, hostingRequest("DELETE", "/hosting/stacks/site?purgeData=true", ""))
	if !mgr.lastForce || !mgr.lastPull || !mgr.lastPurge {
		t.Fatalf("force=%v pull=%v purge=%v", mgr.lastForce, mgr.lastPull, mgr.lastPurge)
	}
}

func TestHostingErrorMapping(t *testing.T) {
	tests := []struct {
		err    *hosting.Error
		status int
	}{
		{&hosting.Error{Code: hosting.CodeStackNotFound}, http.StatusNotFound},
		{&hosting.Error{Code: hosting.CodeNameTaken, Field: "name", Reason: hosting.ReasonProject}, http.StatusConflict},
		{&hosting.Error{Code: hosting.CodeDomainInUse, Field: "domains"}, http.StatusConflict},
		{&hosting.Error{Code: hosting.CodeManuallyModified}, http.StatusConflict},
		{&hosting.Error{Code: hosting.CodeOperationRunning}, http.StatusConflict},
		{&hosting.Error{Code: hosting.CodeInvalidPHPSetting, Field: "php.timezone"}, http.StatusBadRequest},
		{&hosting.Error{Code: hosting.CodeStorage, Message: "storage error: open /data/compose/x: denied"}, http.StatusInternalServerError},
	}
	for _, tt := range tests {
		h, mgr := newHostingTestHandler(true)
		mgr.err = tt.err
		rec := serveHosting(h, hostingRequest("POST", "/hosting/stacks", `{"name":"site"}`))
		body := decodeBody(t, rec)
		if rec.Code != tt.status || body["code"] != tt.err.Code {
			t.Errorf("%s: got %d %v", tt.err.Code, rec.Code, body)
		}
		if tt.err.Field != "" && body["field"] != tt.err.Field {
			t.Errorf("%s: field = %v", tt.err.Code, body["field"])
		}
		if tt.err.Reason != "" && body["reason"] != tt.err.Reason {
			t.Errorf("%s: reason = %v", tt.err.Code, body["reason"])
		}
		if tt.status == http.StatusInternalServerError && bytes.Contains(rec.Body.Bytes(), []byte("/data/compose")) {
			t.Error("storage error leaks a host path")
		}
	}
}

func TestHostingCheckName(t *testing.T) {
	h, mgr := newHostingTestHandler(true)
	rec := serveHosting(h, hostingRequest("GET", "/hosting/stacks/check-name?name=site", ""))
	if body := decodeBody(t, rec); body["available"] != true {
		t.Fatalf("free name: %v", body)
	}

	mgr.checkError = &hosting.Error{Code: hosting.CodeNameTaken, Reason: hosting.ReasonDirectory}
	rec = serveHosting(h, hostingRequest("GET", "/hosting/stacks/check-name?name=site", ""))
	body := decodeBody(t, rec)
	if rec.Code != http.StatusOK || body["available"] != false || body["reason"] != hosting.ReasonDirectory {
		t.Fatalf("taken name: %d %v", rec.Code, body)
	}
}

func TestHostingGetMasksSecretsForReadOnlyKeys(t *testing.T) {
	h, _ := newHostingTestHandler(true)
	for _, tt := range []struct {
		name   string
		scopes []string
		masked bool
	}{
		{"browser session", nil, false},
		{"read-only key", []string{"hosting:read"}, true},
		{"write key", []string{"hosting:read", "hosting:write"}, false},
		{"wildcard key", []string{"*"}, false},
	} {
		rec := serveHosting(h, hostingRequest("GET", "/hosting/stacks/site", "", tt.scopes...))
		leaked := bytes.Contains(rec.Body.Bytes(), []byte("s3cret")) || bytes.Contains(rec.Body.Bytes(), []byte("auth_basic"))
		body := decodeBody(t, rec)
		if rec.Code != http.StatusOK || leaked == tt.masked || (body["secretsMasked"] == true) != tt.masked {
			t.Errorf("%s: code=%d leaked=%v body=%v", tt.name, rec.Code, leaked, body)
		}
		if !bytes.Contains(rec.Body.Bytes(), []byte("DB_PASSWORD")) {
			t.Errorf("%s: variable names must stay visible", tt.name)
		}
	}
}
