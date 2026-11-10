package handler

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"host-panel-api/internal/cron"
	"host-panel-api/internal/functions"
	"host-panel-api/internal/middleware"
	"host-panel-api/internal/model"
)

// fakeCronFunctions knows a fixed set of functions.
type fakeCronFunctions struct {
	mu    sync.Mutex
	names map[string]bool
}

func (f *fakeCronFunctions) Exists(name string) error {
	if err := functions.ValidateName(name); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.names[name] {
		return &functions.Error{Code: functions.CodeNotFound, Message: "function not found"}
	}
	return nil
}

func (f *fakeCronFunctions) remove(name string) {
	f.mu.Lock()
	delete(f.names, name)
	f.mu.Unlock()
}

func newFunctionCronHandler(t *testing.T) (*CronHandler, *fakeCronFunctions) {
	h := newTestCronHandler(t)
	fns := &fakeCronFunctions{names: map[string]bool{"thumbs": true, "other": true}}
	h.Functions = fns
	return h, fns
}

func withKey(r *http.Request, scopes ...string) *http.Request {
	return r.WithContext(middleware.WithAPIKey(r.Context(), &model.APIKey{ID: "k1", Scopes: scopes}))
}

func invokeJobBody(function, event string, extra map[string]any) []byte {
	cfg := map[string]any{"function": function}
	if event != "" {
		cfg["event"] = event
	}
	p := map[string]any{"name": "nightly", "schedule": "0 3 * * *", "type": "function_invoke", "enabled": true, "functionInvoke": cfg}
	for k, v := range extra {
		p[k] = v
	}
	b, _ := json.Marshal(p)
	return b
}

func createInvokeJob(t *testing.T, h *CronHandler, body []byte, wrap func(*http.Request) *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/cron", bytes.NewReader(body))
	req = wrap(adminCtx(req))
	rr := httptest.NewRecorder()
	h.Create(rr, req)
	return rr
}

func noWrap(r *http.Request) *http.Request { return r }

func errorCode(t *testing.T, rr *httptest.ResponseRecorder) string {
	t.Helper()
	var body map[string]string
	_ = json.Unmarshal(rr.Body.Bytes(), &body)
	return body["code"]
}

func storeInvokeJob(h *CronHandler, id, function string, enabled bool) {
	now := time.Now()
	h.Store.Set(id, &cron.Job{
		ID: id, Name: id, Schedule: "0 3 * * *", Type: cron.JobTypeFunctionInvoke, Enabled: enabled,
		FunctionInvoke: &cron.FunctionInvokeConfig{Function: function, Event: "{}"},
		LastStatus:     cron.JobStatusPending, CreatedAt: now, UpdatedAt: now,
	})
}

func TestCronFunctionInvoke_CreateShape(t *testing.T) {
	h, _ := newFunctionCronHandler(t)
	rr := createInvokeJob(t, h, invokeJobBody("thumbs", "", nil), noWrap)
	if rr.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rr.Code, rr.Body.String())
	}
	var raw map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &raw)
	fi, _ := raw["functionInvoke"].(map[string]any)
	if raw["type"] != "function_invoke" || fi["function"] != "thumbs" || fi["event"] != "{}" {
		t.Fatalf("unexpected body: %s", rr.Body.String())
	}
	for _, k := range []string{"dockerAction", "dockerExec", "http", "disabledReason", "lastSkipped", "lastOutput"} {
		if _, ok := raw[k]; ok {
			t.Fatalf("unexpected field %s: %s", k, rr.Body.String())
		}
	}
}

func TestCronFunctionInvoke_CreateValidation(t *testing.T) {
	cases := []struct {
		name   string
		body   []byte
		wrap   func(*http.Request) *http.Request
		status int
		code   string
	}{
		{"jwt admin", invokeJobBody("thumbs", `{"a":1}`, nil), noWrap, http.StatusCreated, ""},
		{"key with both scopes", invokeJobBody("thumbs", "", nil), func(r *http.Request) *http.Request { return withKey(r, "cron", "functions:invoke") }, http.StatusCreated, ""},
		{"key wildcard", invokeJobBody("thumbs", "", nil), func(r *http.Request) *http.Request { return withKey(r, "*") }, http.StatusCreated, ""},
		{"key without functions:invoke", invokeJobBody("thumbs", "", nil), func(r *http.Request) *http.Request { return withKey(r, "cron", "functions:write") }, http.StatusForbidden, ErrInsufficientScope},
		{"key without cron", invokeJobBody("thumbs", "", nil), func(r *http.Request) *http.Request { return withKey(r, "functions:invoke") }, http.StatusForbidden, ErrInsufficientScope},
		{"missing function", invokeJobBody("ghost", "", nil), noWrap, http.StatusNotFound, functions.CodeNotFound},
		{"invalid name", invokeJobBody("Bad_Name", "", nil), noWrap, http.StatusBadRequest, ErrInvalidRequestBody},
		{"invalid event", invokeJobBody("thumbs", `{"a":`, nil), noWrap, http.StatusBadRequest, ErrInvalidRequestBody},
		{"event too large", invokeJobBody("thumbs", `"`+strings.Repeat("a", cron.MaxFunctionEventBytes)+`"`, nil), noWrap, http.StatusBadRequest, ErrInvalidRequestBody},
		{"period under a minute", invokeJobBody("thumbs", "", map[string]any{"schedule": "@every 30s"}), noWrap, http.StatusBadRequest, ErrCronFreqTooHigh},
		{"missing config", func() []byte {
			b, _ := json.Marshal(map[string]any{"name": "n", "schedule": "* * * * *", "type": "function_invoke"})
			return b
		}(), noWrap, http.StatusBadRequest, ErrInvalidRequestBody},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h, _ := newFunctionCronHandler(t)
			rr := createInvokeJob(t, h, c.body, c.wrap)
			if rr.Code != c.status {
				t.Fatalf("expected %d, got %d: %s", c.status, rr.Code, rr.Body.String())
			}
			if c.code != "" && errorCode(t, rr) != c.code {
				t.Fatalf("expected code %s, got %s", c.code, rr.Body.String())
			}
			if c.status != http.StatusCreated && len(h.Store.List()) != 0 {
				t.Fatal("a refused job was stored")
			}
		})
	}
}

func TestCronFunctionInvoke_RestrictedUserRefused(t *testing.T) {
	h, _ := newFunctionCronHandler(t)
	req := httptest.NewRequest(http.MethodPost, "/cron", bytes.NewReader(invokeJobBody("thumbs", "", nil)))
	req = restrictedCtx(req, []string{"thumbs"})
	rr := httptest.NewRecorder()
	h.Create(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", rr.Code, rr.Body.String())
	}

	// Nor listed, read or run.
	storeInvokeJob(h, "j1", "thumbs", true)
	req = restrictedCtx(httptest.NewRequest(http.MethodGet, "/cron", nil), []string{"thumbs"})
	rr = httptest.NewRecorder()
	h.List(rr, req)
	if strings.Contains(rr.Body.String(), "j1") {
		t.Fatalf("restricted user lists the job: %s", rr.Body.String())
	}
	req = restrictedCtx(httptest.NewRequest(http.MethodPost, "/cron/j1/run", nil), []string{"thumbs"})
	req.SetPathValue("id", "j1")
	rr = httptest.NewRecorder()
	h.RunNow(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("run: expected 403, got %d", rr.Code)
	}
}

func TestCronFunctionInvoke_ModuleDisabled(t *testing.T) {
	h := newTestCronHandler(t) // Functions nil
	rr := createInvokeJob(t, h, invokeJobBody("thumbs", "", nil), noWrap)
	if rr.Code != http.StatusNotFound || errorCode(t, rr) != functions.CodeDisabled {
		t.Fatalf("expected 404 FUNCTIONS_DISABLED, got %d: %s", rr.Code, rr.Body.String())
	}

	storeInvokeJob(h, "j1", "thumbs", true)
	req := adminCtx(httptest.NewRequest(http.MethodPost, "/cron/j1/run", nil))
	req.SetPathValue("id", "j1")
	rr = httptest.NewRecorder()
	h.RunNow(rr, req)
	if rr.Code != http.StatusNotFound || errorCode(t, rr) != functions.CodeDisabled {
		t.Fatalf("run: expected 404 FUNCTIONS_DISABLED, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestCronFunctionInvoke_PerFunctionCap(t *testing.T) {
	h, _ := newFunctionCronHandler(t)
	for i := 0; i < cron.MaxJobsPerFunction; i++ {
		if rr := createInvokeJob(t, h, invokeJobBody("thumbs", "", nil), noWrap); rr.Code != http.StatusCreated {
			t.Fatalf("job %d: %d %s", i, rr.Code, rr.Body.String())
		}
	}
	rr := createInvokeJob(t, h, invokeJobBody("thumbs", "", nil), noWrap)
	if rr.Code != http.StatusConflict || errorCode(t, rr) != ErrFunctionCronLimit {
		t.Fatalf("expected 409 %s, got %d: %s", ErrFunctionCronLimit, rr.Code, rr.Body.String())
	}
	// Another function is not affected.
	if rr := createInvokeJob(t, h, invokeJobBody("other", "", nil), noWrap); rr.Code != http.StatusCreated {
		t.Fatalf("other function: %d", rr.Code)
	}
	// Updating one of the five (the job itself is not counted) is allowed;
	// moving the other function's job to the full one is not.
	var target, otherID string
	for _, j := range h.Store.List() {
		if j.FunctionInvoke.Function == "thumbs" {
			target = j.ID
		} else {
			otherID = j.ID
		}
	}
	if rr := putJob(h, target, invokeJobBody("thumbs", `{"x":1}`, nil), noWrap); rr.Code != http.StatusOK {
		t.Fatalf("update in place: %d %s", rr.Code, rr.Body.String())
	}
	if rr := putJob(h, otherID, invokeJobBody("thumbs", "", nil), noWrap); rr.Code != http.StatusConflict {
		t.Fatalf("move to a full function: %d %s", rr.Code, rr.Body.String())
	}
}

func TestCronFunctionInvoke_ConcurrentCreatesRespectCap(t *testing.T) {
	h, _ := newFunctionCronHandler(t)
	var wg sync.WaitGroup
	for i := 0; i < 3*cron.MaxJobsPerFunction; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			createInvokeJob(t, h, invokeJobBody("thumbs", "", nil), noWrap)
		}()
	}
	wg.Wait()
	if n := len(h.Store.List()); n != cron.MaxJobsPerFunction {
		t.Fatalf("jobs = %d, want %d", n, cron.MaxJobsPerFunction)
	}
}

func putJob(h *CronHandler, id string, body []byte, wrap func(*http.Request) *http.Request) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPut, "/cron/"+id, bytes.NewReader(body))
	req.SetPathValue("id", id)
	req = wrap(adminCtx(req))
	rr := httptest.NewRecorder()
	h.Update(rr, req)
	return rr
}

func patchJob(h *CronHandler, id, body string, wrap func(*http.Request) *http.Request) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPatch, "/cron/"+id, strings.NewReader(body))
	req.SetPathValue("id", id)
	req = wrap(adminCtx(req))
	rr := httptest.NewRecorder()
	h.Patch(rr, req)
	return rr
}

func TestCronFunctionInvoke_UpdateAndPatchScopes(t *testing.T) {
	h, _ := newFunctionCronHandler(t)
	storeInvokeJob(h, "j1", "thumbs", true)
	cronOnly := func(r *http.Request) *http.Request { return withKey(r, "cron") }

	if rr := patchJob(h, "j1", `{"name":"renamed"}`, cronOnly); rr.Code != http.StatusForbidden || errorCode(t, rr) != ErrInsufficientScope {
		t.Fatalf("patch without functions:invoke: %d %s", rr.Code, rr.Body.String())
	}
	// Turning it into an HTTP job also needs the scope (it was one).
	httpBody, _ := json.Marshal(map[string]any{"name": "h", "schedule": "0 * * * *", "type": "http",
		"http": map[string]any{"method": "GET", "url": "https://example.com"}})
	if rr := putJob(h, "j1", httpBody, cronOnly); rr.Code != http.StatusForbidden {
		t.Fatalf("put without functions:invoke: %d %s", rr.Code, rr.Body.String())
	}
	// Turning an HTTP job into one needs it too.
	now := time.Now()
	h.Store.Set("h1", &cron.Job{ID: "h1", Name: "h", Schedule: "0 * * * *", Type: cron.JobTypeHTTP, Enabled: true,
		HTTP: &cron.HTTPConfig{Method: "GET", URL: "https://example.com"}, CreatedAt: now, UpdatedAt: now})
	if rr := patchJob(h, "h1", `{"type":"function_invoke","functionInvoke":{"function":"thumbs"}}`, cronOnly); rr.Code != http.StatusForbidden {
		t.Fatalf("patch to function_invoke without the scope: %d %s", rr.Code, rr.Body.String())
	}
	both := func(r *http.Request) *http.Request { return withKey(r, "cron", "functions:invoke") }
	rr := patchJob(h, "h1", `{"type":"function_invoke","functionInvoke":{"function":"thumbs"}}`, both)
	if rr.Code != http.StatusOK {
		t.Fatalf("patch to function_invoke: %d %s", rr.Code, rr.Body.String())
	}
	got, _ := h.Store.Get("h1")
	if got.FunctionInvoke == nil || got.FunctionInvoke.Event != "{}" {
		t.Fatalf("stored config: %+v", got.FunctionInvoke)
	}
	if rr := patchJob(h, "j1", `{"functionInvoke":{"function":"ghost"}}`, both); rr.Code != http.StatusNotFound || errorCode(t, rr) != functions.CodeNotFound {
		t.Fatalf("patch to a missing function: %d %s", rr.Code, rr.Body.String())
	}
	if rr := patchJob(h, "j1", `{"functionInvoke":{"function":"thumbs","event":"nope"}}`, both); rr.Code != http.StatusBadRequest {
		t.Fatalf("patch with an invalid event: %d %s", rr.Code, rr.Body.String())
	}
}

func TestCronFunctionInvoke_RunNowScope(t *testing.T) {
	h, _ := newFunctionCronHandler(t)
	storeInvokeJob(h, "j1", "thumbs", true)
	run := func(wrap func(*http.Request) *http.Request) *httptest.ResponseRecorder {
		req := wrap(adminCtx(httptest.NewRequest(http.MethodPost, "/cron/j1/run", nil)))
		req.SetPathValue("id", "j1")
		rr := httptest.NewRecorder()
		h.RunNow(rr, req)
		return rr
	}
	if rr := run(func(r *http.Request) *http.Request { return withKey(r, "cron") }); rr.Code != http.StatusForbidden || errorCode(t, rr) != ErrInsufficientScope {
		t.Fatalf("run without functions:invoke: %d %s", rr.Code, rr.Body.String())
	}
	if rr := run(func(r *http.Request) *http.Request { return withKey(r, "cron", "functions:invoke") }); rr.Code != http.StatusAccepted {
		t.Fatalf("run: %d %s", rr.Code, rr.Body.String())
	}
	// The executor has no functions module in this test: the run fails,
	// it is recorded as such.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if j, _ := h.Store.Get("j1"); j.LastStatus == cron.JobStatusError {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("the run was not recorded")
}

func TestCronFunctionInvoke_FunctionDeleted(t *testing.T) {
	h, fns := newFunctionCronHandler(t)
	storeInvokeJob(h, "j1", "thumbs", true)
	storeInvokeJob(h, "j2", "other", true)
	if err := h.Scheduler.UpdateJob(mustGet(t, h, "j1")); err != nil {
		t.Fatal(err)
	}

	fns.remove("thumbs")
	h.FunctionDeleted("thumbs", "admin@test.com")

	j1 := mustGet(t, h, "j1")
	if j1.Enabled || j1.DisabledReason != cron.DisabledReasonFunctionDeleted {
		t.Fatalf("j1: enabled=%v reason=%q", j1.Enabled, j1.DisabledReason)
	}
	if j2 := mustGet(t, h, "j2"); !j2.Enabled || j2.DisabledReason != "" {
		t.Fatal("j2 changed")
	}

	// GET shows the reason.
	req := adminCtx(httptest.NewRequest(http.MethodGet, "/cron/j1", nil))
	req.SetPathValue("id", "j1")
	rr := httptest.NewRecorder()
	h.Get(rr, req)
	if !strings.Contains(rr.Body.String(), `"disabledReason":"FUNCTION_DELETED"`) {
		t.Fatalf("GET: %s", rr.Body.String())
	}

	// A disabled job on its deleted function can still be renamed; it
	// cannot be enabled again until it targets an existing function.
	if rr := patchJob(h, "j1", `{"name":"kept"}`, noWrap); rr.Code != http.StatusOK {
		t.Fatalf("rename: %d %s", rr.Code, rr.Body.String())
	}
	if j := mustGet(t, h, "j1"); j.DisabledReason != cron.DisabledReasonFunctionDeleted {
		t.Fatal("the reason was dropped by a rename")
	}
	if rr := patchJob(h, "j1", `{"enabled":true}`, noWrap); rr.Code != http.StatusNotFound || errorCode(t, rr) != functions.CodeNotFound {
		t.Fatalf("enable: %d %s", rr.Code, rr.Body.String())
	}
	rr = patchJob(h, "j1", `{"enabled":true,"functionInvoke":{"function":"other"}}`, noWrap)
	if rr.Code != http.StatusOK {
		t.Fatalf("retarget: %d %s", rr.Code, rr.Body.String())
	}
	if j := mustGet(t, h, "j1"); !j.Enabled || j.DisabledReason != "" {
		t.Fatalf("after retarget: enabled=%v reason=%q", j.Enabled, j.DisabledReason)
	}
}

func mustGet(t *testing.T, h *CronHandler, id string) *cron.Job {
	t.Helper()
	j, ok := h.Store.Get(id)
	if !ok {
		t.Fatalf("job %s missing", id)
	}
	return j
}

// The job JSON as returned by GET /cron/{id} (contract of the interface
// and the CLI).
func TestCronFunctionInvoke_GetShape(t *testing.T) {
	h, _ := newFunctionCronHandler(t)
	ran := time.Date(2026, 11, 6, 3, 0, 0, 0, time.UTC)
	h.Store.Set("j1", &cron.Job{
		ID: "j1", Name: "nightly", Schedule: "0 3 * * *", Type: cron.JobTypeFunctionInvoke, Enabled: true,
		FunctionInvoke: &cron.FunctionInvokeConfig{Function: "thumbs", Event: `{"bucket":"media"}`},
		LastRun:        &ran, LastStatus: cron.JobStatusSuccess,
		LastOutput: `{"statusCode":200,"durationMs":12,"payloadBytes":34}`,
		CreatedAt:  ran, UpdatedAt: ran,
	})
	req := adminCtx(httptest.NewRequest(http.MethodGet, "/cron/j1", nil))
	req.SetPathValue("id", "j1")
	rr := httptest.NewRecorder()
	h.Get(rr, req)
	want := fmt.Sprintf(`{"id":"j1","name":"nightly","schedule":"0 3 * * *","type":"function_invoke","enabled":true,"once":false,`+
		`"functionInvoke":{"function":"thumbs","event":"{\"bucket\":\"media\"}"},"lastRun":%q,"lastStatus":"success",`+
		`"lastOutput":"{\"statusCode\":200,\"durationMs\":12,\"payloadBytes\":34}","createdAt":%[1]q,"updatedAt":%[1]q}`,
		ran.Format(time.RFC3339))
	if strings.TrimSpace(rr.Body.String()) != want {
		t.Fatalf("GET body:\n%s\nwant:\n%s", rr.Body.String(), want)
	}
}

func TestCronFunctionInvoke_ChangesWithModuleDisabled(t *testing.T) {
	h, _ := newFunctionCronHandler(t)
	storeInvokeJob(h, "j1", "thumbs", true)
	h.Functions = nil
	for _, body := range []string{`{"enabled":false}`, `{"name":"renamed"}`, `{"schedule":"0 4 * * *"}`} {
		if rr := patchJob(h, "j1", body, noWrap); rr.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", body, rr.Code, rr.Body.String())
		}
	}
	if rr := patchJob(h, "j1", `{"enabled":true}`, noWrap); rr.Code != http.StatusNotFound || errorCode(t, rr) != functions.CodeDisabled {
		t.Fatalf("enable: %d %s", rr.Code, rr.Body.String())
	}
	if rr := patchJob(h, "j1", `{"functionInvoke":{"function":"other"}}`, noWrap); rr.Code != http.StatusNotFound {
		t.Fatalf("retarget: %d %s", rr.Code, rr.Body.String())
	}
	// Turning it into an HTTP job needs no module.
	if rr := patchJob(h, "j1", `{"type":"http","http":{"method":"GET","url":"https://example.com"}}`, noWrap); rr.Code != http.StatusOK {
		t.Fatalf("type switch: %d %s", rr.Code, rr.Body.String())
	}
}

func TestCronFunctionInvoke_TypeSwitchClearsReasonAndConfig(t *testing.T) {
	h, fns := newFunctionCronHandler(t)
	storeInvokeJob(h, "j1", "thumbs", true)
	fns.remove("thumbs")
	h.FunctionDeleted("thumbs", "a")
	rr := patchJob(h, "j1", `{"type":"http","http":{"method":"GET","url":"https://example.com"}}`, noWrap)
	if rr.Code != http.StatusOK {
		t.Fatalf("%d %s", rr.Code, rr.Body.String())
	}
	j := mustGet(t, h, "j1")
	if j.DisabledReason != "" || j.FunctionInvoke != nil || j.Type != cron.JobTypeHTTP {
		t.Fatalf("reason=%q fi=%v type=%s", j.DisabledReason, j.FunctionInvoke, j.Type)
	}
}

// A change keeps the execution fields of the live job (a run may end
// between the read and the write).
func TestCronFunctionInvoke_WriteKeepsLiveRunFields(t *testing.T) {
	h, _ := newFunctionCronHandler(t)
	storeInvokeJob(h, "j1", "thumbs", true)
	ran := time.Date(2026, 11, 6, 3, 0, 0, 0, time.UTC)
	h.Store.Update("j1", func(j *cron.Job) {
		j.LastRun, j.LastStatus, j.LastOutput = &ran, cron.JobStatusSuccess, `{"statusCode":200,"durationMs":1,"payloadBytes":0}`
	})
	stale := mustGet(t, h, "j1")
	stale.Name = "renamed"
	stale.LastStatus = cron.JobStatusRunning
	if !h.writeJob("j1", stale) {
		t.Fatal("writeJob")
	}
	got := mustGet(t, h, "j1")
	if got.Name != "renamed" || got.LastStatus != cron.JobStatusSuccess || got.LastRun == nil || stale.LastStatus != cron.JobStatusSuccess {
		t.Fatalf("got %+v", got)
	}
	h.Store.Delete("j1")
	if h.writeJob("j1", stale) {
		t.Fatal("writeJob recreated a deleted job")
	}
}
