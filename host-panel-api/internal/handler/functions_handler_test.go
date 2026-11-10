package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"host-panel-api/internal/cron"
	"host-panel-api/internal/functions"
	"host-panel-api/internal/middleware"
	"host-panel-api/internal/model"

	"github.com/moby/moby/api/types/container"
)

type mockFunctionsManager struct {
	err       error
	lastActor string
	uploaded  []byte
	secret    [3]string
	event     []byte
	action    string
	result    functions.InvokeResult
	purged    bool
	sigv4     *bool
}

func (m *mockFunctionsManager) Info() functions.Info { return functions.Info{Arch: "arm64", Max: 50} }
func (m *mockFunctionsManager) List(context.Context) ([]functions.Summary, error) {
	return []functions.Summary{{Name: "resize"}}, m.err
}
func (m *mockFunctionsManager) Get(_ context.Context, name string) (*functions.Detail, error) {
	if m.err != nil {
		return nil, m.err
	}
	return &functions.Detail{Summary: functions.Summary{Name: name}, Secrets: []string{"API_KEY"}}, nil
}
func (m *mockFunctionsManager) Create(_ context.Context, req functions.CreateRequest, actor string) (*functions.Created, error) {
	m.lastActor = actor
	if m.err != nil {
		return nil, m.err
	}
	return &functions.Created{Function: &functions.Detail{Summary: functions.Summary{Name: req.Name}},
		Credentials: &functions.Credentials{Token: "tok"}}, nil
}
func (m *mockFunctionsManager) Update(_ context.Context, name string, _ functions.UpdateRequest, actor string) (*functions.Detail, error) {
	m.lastActor = actor
	return &functions.Detail{Summary: functions.Summary{Name: name}}, m.err
}
func (m *mockFunctionsManager) Delete(context.Context, string, string) (*functions.DeleteResult, error) {
	return &functions.DeleteResult{}, m.err
}
func (m *mockFunctionsManager) Purge(context.Context, string, string) (*functions.DeleteResult, error) {
	m.purged = true
	return &functions.DeleteResult{}, m.err
}
func (m *mockFunctionsManager) Deploy(_ context.Context, _ string, upload io.Reader, _ string) (*functions.Deployment, error) {
	data, err := io.ReadAll(upload)
	m.uploaded = data
	if err != nil {
		return nil, &functions.Error{Code: functions.CodeInvalidCode, Message: "the upload was interrupted"}
	}
	return &functions.Deployment{ID: "20261106T120000Z-0123abcd"}, m.err
}
func (m *mockFunctionsManager) Deployments(context.Context, string) ([]functions.DeploymentInfo, error) {
	return nil, m.err
}
func (m *mockFunctionsManager) Activate(context.Context, string, string, string) error { return m.err }
func (m *mockFunctionsManager) SetSecret(_ context.Context, name, key, value, _ string) error {
	m.secret = [3]string{name, key, value}
	return m.err
}
func (m *mockFunctionsManager) DeleteSecret(context.Context, string, string, string) error {
	return m.err
}
func (m *mockFunctionsManager) RotateCredentials(_ context.Context, _ string, sigv4 *bool, _ string) (*functions.Credentials, error) {
	m.sigv4 = sigv4
	return &functions.Credentials{Token: "new"}, m.err
}
func (m *mockFunctionsManager) Invoke(_ context.Context, _ string, event io.Reader, _ string) (*functions.InvokeResult, error) {
	data, err := io.ReadAll(event)
	m.event = data
	var mbe *http.MaxBytesError
	if errors.As(err, &mbe) {
		return nil, &functions.Error{Code: functions.CodeEventTooLarge, Message: "too large"}
	}
	if errors.Is(err, os.ErrDeadlineExceeded) { // as the manager maps it
		return nil, &functions.Error{Code: functions.CodeEventTimeout, Message: "the event was not received in time"}
	}
	if m.err != nil {
		return nil, m.err
	}
	r := m.result
	return &r, nil
}
func (m *mockFunctionsManager) Action(_ context.Context, _, action, _ string) error {
	m.action = action
	return m.err
}
func (m *mockFunctionsManager) SetNetworks(_ context.Context, _ string, nets []string, _ string) ([]string, error) {
	return nets, m.err
}
func (m *mockFunctionsManager) SetEgress(context.Context, string, bool, string) error { return m.err }

func newFunctionsTestHandler(enabled bool) (*FunctionsHandler, *mockFunctionsManager) {
	mgr := &mockFunctionsManager{result: functions.InvokeResult{StatusCode: 200, RequestID: "abc", Payload: []byte(`{"ok":true}`)}}
	h := &FunctionsHandler{Enabled: enabled, MaxCodeBytes: 1 << 10, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	if enabled {
		h.Manager = mgr
	}
	return h, mgr
}

func serveFunctions(h *FunctionsHandler, req *http.Request) *httptest.ResponseRecorder {
	mux := http.NewServeMux()
	routes := map[string]http.HandlerFunc{
		"GET /functions/config":                            h.Config,
		"GET /functions":                                   h.List,
		"POST /functions":                                  h.Create,
		"GET /functions/{name}":                            h.Get,
		"PATCH /functions/{name}":                          h.Update,
		"DELETE /functions/{name}":                         h.Delete,
		"POST /functions/{name}/code":                      h.Deploy,
		"GET /functions/{name}/deployments":                h.Deployments,
		"POST /functions/{name}/deployments/{id}/activate": h.Activate,
		"PUT /functions/{name}/secrets/{key}":              h.SetSecret,
		"DELETE /functions/{name}/secrets/{key}":           h.DeleteSecret,
		"POST /functions/{name}/credentials/rotate":        h.RotateCredentials,
		"POST /functions/{name}/invoke":                    h.Invoke,
		"POST /functions/{name}/{action}":                  h.Action,
		"PUT /functions/{name}/networks":                   h.SetNetworks,
		"PUT /functions/{name}/egress":                     h.SetEgress,
	}
	for pattern, fn := range routes {
		// Admin only, as wired in cmd/server.
		mux.Handle(pattern, middleware.Admin()(fn))
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func zipRequest(target string, body []byte, scopes ...string) *http.Request {
	req := hostingRequest("POST", target, string(body), scopes...)
	req.Header.Set("Content-Type", "application/zip")
	return req
}

var functionsRoutes = []struct{ method, path, body, scope string }{
	{"GET", "/functions", "", "functions:read"},
	{"POST", "/functions", `{"name":"resize","runtime":"nodejs","runtimeVersion":"22","handler":"index.handler"}`, "functions:write"},
	{"GET", "/functions/resize", "", "functions:read"},
	{"PATCH", "/functions/resize", `{"memoryMB":256}`, "functions:write"},
	{"DELETE", "/functions/resize", "", "functions:write"},
	{"GET", "/functions/resize/deployments", "", "functions:read"},
	{"POST", "/functions/resize/deployments/20261106T120000Z-0123abcd/activate", "", "functions:deploy"},
	{"PUT", "/functions/resize/secrets/API_KEY", `{"value":"v"}`, "functions:deploy"},
	{"DELETE", "/functions/resize/secrets/API_KEY", "", "functions:deploy"},
	{"POST", "/functions/resize/credentials/rotate", "", "functions:deploy"},
	{"POST", "/functions/resize/invoke", `{}`, "functions:invoke"},
	{"POST", "/functions/resize/restart", "", "functions:write"},
	{"PATCH", "/functions/resize", `{"env":[{"name":"API_URL","value":"https://x"}]}`, "functions:deploy+functions:write"},
	{"PATCH", "/functions/resize", `{"handler":"other.handler"}`, "functions:deploy+functions:write"},
	{"PATCH", "/functions/resize", `{"mcpInvocable":true}`, "functions:deploy+functions:write"},
	{"PATCH", "/functions/resize", `{"mcpInvocable":false}`, "functions:deploy+functions:write"},
	{"POST", "/functions", `{"name":"resize","runtime":"nodejs","runtimeVersion":"22","handler":"index.handler","mcpInvocable":true}`, "functions:deploy+functions:write"},
	{"POST", "/functions", `{"name":"resize","runtime":"nodejs","runtimeVersion":"22","handler":"index.handler","egress":true}`, "functions:deploy+functions:write"},
	{"POST", "/functions", `{"name":"resize","runtime":"nodejs","runtimeVersion":"22","handler":"index.handler","sigv4":true}`, "functions:deploy+functions:write"},
	{"PUT", "/functions/resize/networks", `{"networks":["fn-resize"]}`, "functions:deploy"},
	{"PUT", "/functions/resize/egress", `{"enabled":true}`, "functions:deploy"},
}

func TestFunctionsScopes(t *testing.T) {
	h, _ := newFunctionsTestHandler(true)
	all := []string{"functions:read", "functions:write", "functions:deploy", "functions:invoke"}
	for _, rt := range functionsRoutes {
		need := strings.Split(rt.scope, "+")
		if len(need) > 1 {
			// Both are needed: each alone is refused.
			for _, one := range need {
				if rec := serveFunctions(h, hostingRequest(rt.method, rt.path, rt.body, one)); rec.Code != http.StatusForbidden {
					t.Fatalf("%s %s %s with %s only: %d", rt.method, rt.path, rt.body, one, rec.Code)
				}
			}
			if rec := serveFunctions(h, hostingRequest(rt.method, rt.path, rt.body, need...)); rec.Code >= 300 {
				t.Fatalf("%s %s with %v: %d %s", rt.method, rt.path, need, rec.Code, rec.Body)
			}
			continue
		}
		// Every other functions scope is refused: none implies another
		// (functions:invoke in particular).
		var others []string
		for _, s := range all {
			if s != rt.scope {
				others = append(others, s)
			}
		}
		if rec := serveFunctions(h, hostingRequest(rt.method, rt.path, rt.body, others...)); rec.Code != http.StatusForbidden {
			t.Fatalf("%s %s with %v: %d", rt.method, rt.path, others, rec.Code)
		}
		if rec := serveFunctions(h, hostingRequest(rt.method, rt.path, rt.body, rt.scope)); rec.Code >= 300 {
			t.Fatalf("%s %s with %s: %d %s", rt.method, rt.path, rt.scope, rec.Code, rec.Body)
		}
		if rec := serveFunctions(h, hostingRequest(rt.method, rt.path, rt.body, "*")); rec.Code >= 300 {
			t.Fatalf("%s %s with *: %d", rt.method, rt.path, rec.Code)
		}
	}
	// The code upload needs functions:deploy.
	if rec := serveFunctions(h, zipRequest("/functions/resize/code", []byte("PK"), "functions:write")); rec.Code != http.StatusForbidden {
		t.Fatalf("upload with functions:write: %d", rec.Code)
	}
}

func TestFunctionsAdminOnly(t *testing.T) {
	h, _ := newFunctionsTestHandler(true)
	for _, rt := range append(functionsRoutes, struct{ method, path, body, scope string }{"GET", "/functions/config", "", ""}) {
		req := httptest.NewRequest(rt.method, rt.path, strings.NewReader(rt.body))
		req = req.WithContext(middleware.WithUser(req.Context(), &model.User{Email: "u@example.com", Profile: model.ProfileRestricted}))
		if rec := serveFunctions(h, req); rec.Code != http.StatusForbidden {
			t.Fatalf("%s %s as a restricted user: %d", rt.method, rt.path, rec.Code)
		}
	}
}

func TestFunctionsDisabled(t *testing.T) {
	h, _ := newFunctionsTestHandler(false)
	for _, rt := range functionsRoutes {
		rec := serveFunctions(h, hostingRequest(rt.method, rt.path, rt.body))
		if rec.Code != http.StatusNotFound || decodeBody(t, rec)["code"] != functions.CodeDisabled {
			t.Fatalf("%s %s: %d %s", rt.method, rt.path, rec.Code, rec.Body)
		}
	}
	h.ConfigError = FunctionsConfigErrorCode
	body := decodeBody(t, serveFunctions(h, hostingRequest("GET", "/functions/config", "")))
	if body["enabled"] != false || body["error"] != FunctionsConfigErrorCode {
		t.Fatalf("config: %v", body)
	}
	on, _ := newFunctionsTestHandler(true)
	body = decodeBody(t, serveFunctions(on, hostingRequest("GET", "/functions/config", "")))
	if body["enabled"] != true || body["config"].(map[string]any)["arch"] != "arm64" {
		t.Fatalf("config: %v", body)
	}
}

func TestFunctionsCreateReturnsCredentialsOnce(t *testing.T) {
	h, mgr := newFunctionsTestHandler(true)
	rec := serveFunctions(h, hostingRequest("POST", "/functions", `{"name":"resize","runtime":"nodejs","runtimeVersion":"22","handler":"index.handler"}`))
	if rec.Code != http.StatusCreated || rec.Header().Get("Cache-Control") != "no-store" || mgr.lastActor != "admin@example.com" {
		t.Fatalf("%d %v", rec.Code, rec.Header())
	}
	rec = serveFunctions(h, hostingRequest("POST", "/functions/resize/credentials/rotate", ""))
	if rec.Code != http.StatusOK || rec.Header().Get("Cache-Control") != "no-store" || mgr.sigv4 != nil {
		t.Fatalf("rotate: %d %v", rec.Code, rec.Header())
	}
	rec = serveFunctions(h, hostingRequest("POST", "/functions/resize/credentials/rotate", `{"sigv4":true}`))
	if rec.Code != http.StatusOK || mgr.sigv4 == nil || !*mgr.sigv4 {
		t.Fatalf("rotate with sigv4: %d", rec.Code)
	}
	// Unknown fields are refused.
	if rec := serveFunctions(h, hostingRequest("POST", "/functions", `{"name":"x","privileged":true}`)); rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown field: %d", rec.Code)
	}
}

func TestFunctionsSecretNeverEchoed(t *testing.T) {
	h, mgr := newFunctionsTestHandler(true)
	rec := serveFunctions(h, hostingRequest("PUT", "/functions/resize/secrets/API_KEY", `{"value":"s3cr3t"}`))
	if rec.Code != http.StatusNoContent || rec.Body.Len() != 0 || mgr.secret != [3]string{"resize", "API_KEY", "s3cr3t"} {
		t.Fatalf("%d %q %v", rec.Code, rec.Body, mgr.secret)
	}
	mgr.err = &functions.Error{Code: functions.CodeInvalidSecret, Field: "value", Message: "a value is one line of UTF-8 text"}
	rec = serveFunctions(h, hostingRequest("PUT", "/functions/resize/secrets/API_KEY", `{"value":"s3cr3t"}`))
	if rec.Code != http.StatusBadRequest || strings.Contains(rec.Body.String(), "s3cr3t") {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if rec := serveFunctions(h, hostingRequest("PUT", "/functions/resize/secrets/API_KEY", `{}`)); rec.Code != http.StatusBadRequest {
		t.Fatalf("missing value: %d", rec.Code)
	}
}

func TestFunctionsInvokeEnvelope(t *testing.T) {
	h, mgr := newFunctionsTestHandler(true)
	mgr.result = functions.InvokeResult{StatusCode: 200, FunctionError: "Unhandled", RequestID: "abc",
		Payload: []byte(`<script>alert(1)</script>`), LogTail: []byte("log \xff line")}
	rec := serveFunctions(h, hostingRequest("POST", "/functions/resize/invoke", `{"key":"value"}`))
	if rec.Code != http.StatusOK || string(mgr.event) != `{"key":"value"}` {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	hd := rec.Header()
	if hd.Get("Content-Type") != "application/json" || hd.Get("X-Content-Type-Options") != "nosniff" ||
		hd.Get("Cache-Control") != "no-store" || !strings.Contains(hd.Get("Content-Security-Policy"), "sandbox") {
		t.Fatalf("headers: %v", hd)
	}
	if strings.Contains(rec.Body.String(), "<script>") {
		t.Fatal("the payload must be JSON-escaped")
	}
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out["payload"] != `<script>alert(1)</script>` || out["functionError"] != "Unhandled" || out["logTail"] != "log � line" {
		t.Fatalf("envelope: %v", out)
	}
	// A binary payload is base64.
	mgr.result = functions.InvokeResult{StatusCode: 200, Payload: []byte{0xff, 0x00}}
	out = decodeBody(t, serveFunctions(h, hostingRequest("POST", "/functions/resize/invoke", `{}`)))
	if out["payload"] != nil || out["payloadBase64"] != "/wA=" {
		t.Fatalf("binary payload: %v", out)
	}
	// Event over 6 MiB.
	big := hostingRequest("POST", "/functions/resize/invoke", strings.Repeat("x", functions.MaxEventBytes+1))
	if rec := serveFunctions(h, big); rec.Code != http.StatusRequestEntityTooLarge || decodeBody(t, rec)["code"] != functions.CodeEventTooLarge {
		t.Fatalf("big event: %d", rec.Code)
	}
}

func TestFunctionsUpload(t *testing.T) {
	h, mgr := newFunctionsTestHandler(true)
	rec := serveFunctions(h, zipRequest("/functions/resize/code", []byte("PK-zip")))
	if rec.Code != http.StatusCreated || string(mgr.uploaded) != "PK-zip" {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	// Multipart: the "code" part is streamed.
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	if err := mw.WriteField("comment", "ignored"); err != nil {
		t.Fatal(err)
	}
	fw, _ := mw.CreateFormFile("code", "code.zip")
	if _, err := fw.Write([]byte("PK-multipart")); err != nil {
		t.Fatal(err)
	}
	mw.Close()
	req := hostingRequest("POST", "/functions/resize/code", buf.String())
	req.Header.Set("Content-Type", mw.FormDataContentType())
	if rec := serveFunctions(h, req); rec.Code != http.StatusCreated || string(mgr.uploaded) != "PK-multipart" {
		t.Fatalf("multipart: %d %q", rec.Code, mgr.uploaded)
	}
	// Too large (MaxCodeBytes 1 KiB + multipart allowance).
	rec = serveFunctions(h, zipRequest("/functions/resize/code", bytes.Repeat([]byte("x"), 1<<10+multipartOverhead+1)))
	if rec.Code != http.StatusRequestEntityTooLarge || decodeBody(t, rec)["code"] != functions.CodeCodeTooLarge {
		t.Fatalf("too large: %d %s", rec.Code, rec.Body)
	}
	// Chunked (no Content-Length): the body limit still applies.
	req = zipRequest("/functions/resize/code", bytes.Repeat([]byte("x"), 1<<10+multipartOverhead+1))
	req.ContentLength = -1
	if rec := serveFunctions(h, req); rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("chunked too large: %d %s", rec.Code, rec.Body)
	}
	req = hostingRequest("POST", "/functions/resize/code", "x")
	req.Header.Set("Content-Type", "text/plain")
	if rec := serveFunctions(h, req); rec.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("text/plain: %d", rec.Code)
	}
	req = hostingRequest("POST", "/functions/resize/code", "--x--")
	req.Header.Set("Content-Type", "multipart/form-data; boundary=x")
	if rec := serveFunctions(h, req); rec.Code != http.StatusBadRequest {
		t.Fatalf("multipart without code: %d", rec.Code)
	}
}

func TestFunctionsPurgeAndActions(t *testing.T) {
	h, mgr := newFunctionsTestHandler(true)
	if rec := serveFunctions(h, hostingRequest("DELETE", "/functions/resize?purge=true", "")); rec.Code != http.StatusOK || !mgr.purged {
		t.Fatalf("purge: %d", rec.Code)
	}
	if rec := serveFunctions(h, hostingRequest("DELETE", "/functions/resize?purge=yes", "")); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad purge: %d", rec.Code)
	}
	for _, a := range []string{"start", "stop", "restart"} {
		if rec := serveFunctions(h, hostingRequest("POST", "/functions/resize/"+a, "")); rec.Code != http.StatusNoContent || mgr.action != a {
			t.Fatalf("%s: %d", a, rec.Code)
		}
	}
	if rec := serveFunctions(h, hostingRequest("PUT", "/functions/resize/egress", `{}`)); rec.Code != http.StatusBadRequest {
		t.Fatalf("egress without enabled: %d", rec.Code)
	}
}

func TestFunctionsErrorCodesAreMapped(t *testing.T) {
	for _, code := range functions.AllCodes {
		status, ok := functionsErrorStatus[code]
		if !ok {
			t.Errorf("%s is not mapped", code)
		}
		h, mgr := newFunctionsTestHandler(true)
		mgr.err = &functions.Error{Code: code, Message: "/data/functions/.loch/fn-resize is not owned by the panel"}
		rec := serveFunctions(h, hostingRequest("GET", "/functions/resize", ""))
		body := decodeBody(t, rec)
		if rec.Code != status || body["code"] != code {
			t.Errorf("%s: %d %v", code, rec.Code, body)
		}
		// Internal messages never reach the client.
		if (functions.InternalCodes[code] || status == http.StatusInternalServerError) && strings.Contains(rec.Body.String(), "/data/functions") {
			t.Errorf("%s leaks its message: %s", code, rec.Body)
		}
	}
	h, mgr := newFunctionsTestHandler(true)
	mgr.err = errors.New("boom /secret/path")
	rec := serveFunctions(h, hostingRequest("GET", "/functions/resize", ""))
	if rec.Code != http.StatusInternalServerError || strings.Contains(rec.Body.String(), "/secret/path") {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}

func TestFunctionContainersNeverGranted(t *testing.T) {
	restricted := &model.User{Profile: model.ProfileRestricted, Permissions: []string{"fn-resize", "web"}}
	if userCanAccessContainer(restricted, "/fn-resize") || !userCanAccessContainer(restricted, "/web") {
		t.Fatal("a function container must never be granted to a restricted profile")
	}
	list := filterByPermissions([]container.Summary{{Names: []string{"/fn-resize"}}, {Names: []string{"/web"}}}, restricted.Permissions)
	if len(list) != 1 || list[0].Names[0] != "/web" {
		t.Fatalf("filtered: %v", list)
	}
	// Granting one is refused.
	store := newMockUserStore()
	store.Set("u@example.com", &model.User{Email: "u@example.com", Profile: model.ProfileRestricted})
	uh := &UsersHandler{Store: store, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Audit: slog.New(slog.NewTextHandler(io.Discard, nil))}
	req := hostingRequest("PATCH", "/users/u@example.com", `{"permissions":["web","fn-resize"]}`)
	req.SetPathValue("email", "u@example.com")
	rec := httptest.NewRecorder()
	uh.UpdateACL(rec, req)
	if rec.Code != http.StatusBadRequest || decodeBody(t, rec)["code"] != ErrFunctionProtected {
		t.Fatalf("grant: %d %s", rec.Code, rec.Body)
	}
	// No cron command in a function container.
	job := &cron.Job{Name: "x", Type: cron.JobTypeDockerExec, Schedule: "*/5 * * * *",
		DockerExec: &cron.DockerExecConfig{ContainerName: "fn-resize", Command: []string{"env"}}}
	if status, code, _ := validateJobSchedule(job); status != http.StatusForbidden || code != ErrFunctionProtected {
		t.Fatalf("cron exec: %d %s", status, code)
	}
}

// A stalled or trickling upload is cut (over a real connection: read
// deadlines need one), so it never holds the function lock for long.
func TestFunctionsUploadPacing(t *testing.T) {
	h, _ := newFunctionsTestHandler(true)
	h.MaxCodeBytes = 1 << 20
	h.UploadStall, h.UploadGrace, h.UploadMinRate = 200*time.Millisecond, 300*time.Millisecond, 64<<10
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r = r.WithContext(middleware.WithUser(r.Context(), &model.User{Email: "admin@example.com", Profile: model.ProfileAdmin}))
		r.SetPathValue("name", "resize")
		h.Deploy(w, r)
	}))
	defer srv.Close()
	post := func(body io.Reader) (int, time.Duration) {
		start := time.Now()
		req, _ := http.NewRequest("POST", srv.URL+"/functions/resize/code", body)
		req.Header.Set("Content-Type", "application/zip")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return 0, time.Since(start)
		}
		resp.Body.Close()
		return resp.StatusCode, time.Since(start)
	}
	// Never written.
	pr, pw := io.Pipe()
	defer pw.Close()
	if code, d := post(pr); code != http.StatusRequestTimeout || d > 3*time.Second {
		t.Fatalf("stalled upload: %d after %v", code, d)
	}
	// A trickle: one byte every 50 ms (well under the minimum rate).
	tr, tw := io.Pipe()
	go func() {
		for range 200 {
			if _, err := tw.Write([]byte("x")); err != nil {
				return
			}
			time.Sleep(50 * time.Millisecond)
		}
		tw.Close()
	}()
	if code, d := post(tr); code != http.StatusRequestTimeout || d > 3*time.Second {
		t.Fatalf("trickling upload: %d after %v", code, d)
	}
	tw.CloseWithError(io.ErrClosedPipe)
	// A normal upload still works.
	if code, _ := post(bytes.NewReader(bytes.Repeat([]byte("x"), 100<<10))); code != http.StatusCreated {
		t.Fatalf("normal upload: %d", code)
	}
}

func TestFunctionNetworkMembersNeedDeploy(t *testing.T) {
	h, _ := newNetworksTestHandler()
	for _, tc := range []struct {
		scopes []string
		want   int
	}{{[]string{"networks:write"}, http.StatusForbidden}, {[]string{"networks:write", "functions:deploy"}, 0}} {
		rec := serveNetworks(h, hostingRequest("POST", "/networks/images/members", `{"kind":"function","name":"resize"}`, tc.scopes...))
		if tc.want != 0 && rec.Code != tc.want || tc.want == 0 && rec.Code == http.StatusForbidden {
			t.Fatalf("attach with %v: %d", tc.scopes, rec.Code)
		}
		rec = serveNetworks(h, hostingRequest("DELETE", "/networks/images/members/function/resize", "", tc.scopes...))
		if tc.want != 0 && rec.Code != tc.want || tc.want == 0 && rec.Code == http.StatusForbidden {
			t.Fatalf("detach with %v: %d", tc.scopes, rec.Code)
		}
	}
	// Other kinds keep networks:write alone.
	if rec := serveNetworks(h, hostingRequest("POST", "/networks/images/members", `{"kind":"site","name":"shop"}`, "networks:write")); rec.Code == http.StatusForbidden {
		t.Fatalf("site attach: %d", rec.Code)
	}
}

func TestFunctionStacksAreProtected(t *testing.T) {
	h := &StackHandler{Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Audit: slog.New(slog.NewTextHandler(io.Discard, nil))}
	req := hostingRequest("POST", "/stacks/fn-resize/stop", "")
	req.SetPathValue("name", "fn-resize")
	req.SetPathValue("action", "stop")
	rec := httptest.NewRecorder()
	h.Action(rec, req)
	if rec.Code != http.StatusForbidden || decodeBody(t, rec)["code"] != ErrFunctionProtected {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}

// Over a real connection: an event that never arrives is cut (408), and an
// error answered before the body is read reaches the client at once, with
// the connection closed (net/http would drain the body first).
func TestFunctionsUnreadBodies(t *testing.T) {
	h, _ := newFunctionsTestHandler(true)
	h.EventReadTimeout = 200 * time.Millisecond
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r = r.WithContext(middleware.WithUser(r.Context(), &model.User{Email: "admin@example.com", Profile: model.ProfileAdmin}))
		r.SetPathValue("name", "resize")
		if strings.HasSuffix(r.URL.Path, "/invoke") {
			h.Invoke(w, r)
			return
		}
		h.Deploy(w, r)
	}))
	defer srv.Close()
	post := func(path, contentType string) (*http.Response, time.Duration) {
		pr, pw := io.Pipe()
		t.Cleanup(func() { pw.Close() })
		// a start, then nothing; the write fails once the pipe is closed at cleanup, which is expected
		go func() { _, _ = pw.Write([]byte("{")) }()
		req, _ := http.NewRequest("POST", srv.URL+path, pr)
		req.Header.Set("Content-Type", contentType)
		start := time.Now()
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		resp.Body.Close()
		return resp, time.Since(start)
	}
	if resp, d := post("/functions/resize/invoke", "application/json"); resp.StatusCode != http.StatusRequestTimeout || d > 3*time.Second || !resp.Close {
		t.Fatalf("stalled event: %d after %v, %v", resp.StatusCode, d, resp.Header)
	}
	if resp, d := post("/functions/resize/code", "text/plain"); resp.StatusCode != http.StatusUnsupportedMediaType || d > 3*time.Second || !resp.Close {
		t.Fatalf("early deploy error: %d after %v", resp.StatusCode, d)
	}
}

func TestFunctionContainersExcludedEverywhere(t *testing.T) {
	list := filterByPermissions([]container.Summary{
		{Names: []string{"/renamed"}, Labels: map[string]string{"sh.loch.function": "resize"}},
		{Names: []string{"/web"}, Labels: map[string]string{"com.docker.compose.project": "fn-resize"}},
		{Names: []string{"/ok"}},
	}, []string{"renamed", "web", "ok"})
	if len(list) != 1 || list[0].Names[0] != "/ok" {
		t.Fatalf("filtered: %v", list)
	}
	admin := &model.User{Profile: model.ProfileAdmin}
	got := selectStackContainers([]container.Summary{
		{ID: "a", Names: []string{"/x"}, Labels: map[string]string{"com.docker.compose.project": "shop", "sh.loch.function": "x"}},
		{ID: "b", Names: []string{"/y"}, Labels: map[string]string{"com.docker.compose.project": "shop"}},
	}, "shop", admin, "")
	if len(got) != 1 || got[0].ID != "b" {
		t.Fatalf("stack: %v", got)
	}
	job := &cron.Job{Name: "x", Type: cron.JobTypeDockerAction, Schedule: "*/5 * * * *",
		DockerAction: &cron.DockerActionConfig{ContainerName: "fn-resize", Action: "stop"}}
	if status, code, _ := validateJobSchedule(job); status != http.StatusForbidden || code != ErrFunctionProtected {
		t.Fatalf("cron action: %d %s", status, code)
	}
}
