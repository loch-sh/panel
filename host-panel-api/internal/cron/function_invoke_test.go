package cron

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"host-panel-api/internal/functions"
)

// fakeInvoker records the invocations and answers with res / err. When
// block is set, every invocation waits on it.
type fakeInvoker struct {
	mu      sync.Mutex
	calls   []fakeCall
	res     *functions.InvokeResult
	err     error
	block   chan struct{}
	started chan struct{}
}

type fakeCall struct{ name, event, actor string }

func (f *fakeInvoker) Invoke(ctx context.Context, name string, event io.Reader, actor string) (*functions.InvokeResult, error) {
	body, _ := io.ReadAll(event)
	f.mu.Lock()
	f.calls = append(f.calls, fakeCall{name, string(body), actor})
	f.mu.Unlock()
	if f.started != nil {
		f.started <- struct{}{}
	}
	if f.block != nil {
		<-f.block
	}
	if f.err != nil {
		return nil, f.err
	}
	res := *f.res
	return &res, nil
}

func (f *fakeInvoker) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func newFunctionExecutor(t *testing.T, inv FunctionInvoker) (*Executor, *Store) {
	t.Helper()
	store := newTestStore(t)
	e := &Executor{store: store, logger: testLogger(), audit: testLogger()}
	e.SetFunctions(inv)
	return e, store
}

func functionJob(id, function, event string) *Job {
	return &Job{
		ID: id, Name: "invoke " + function, Schedule: "* * * * *", Type: JobTypeFunctionInvoke, Enabled: true,
		FunctionInvoke: &FunctionInvokeConfig{Function: function, Event: event},
		LastStatus:     JobStatusRunning,
	}
}

func TestJob_Validate_FunctionInvoke(t *testing.T) {
	base := func(cfg *FunctionInvokeConfig) *Job {
		return &Job{Name: "n", Schedule: "* * * * *", Type: JobTypeFunctionInvoke, FunctionInvoke: cfg}
	}
	cases := []struct {
		name string
		job  *Job
		want string // substring of the message, "" = valid
	}{
		{"valid", base(&FunctionInvokeConfig{Function: "thumbs", Event: `{"a":1}`}), ""},
		{"scalar event", base(&FunctionInvokeConfig{Function: "thumbs", Event: `42`}), ""},
		{"missing config", base(nil), "functionInvoke config is required"},
		{"missing function", base(&FunctionInvokeConfig{Event: "{}"}), "functionInvoke.function is required"},
		{"invalid name", base(&FunctionInvokeConfig{Function: "Bad_Name", Event: "{}"}), "not a valid function name"},
		{"invalid event", base(&FunctionInvokeConfig{Function: "thumbs", Event: `{"a":`}), "must be one JSON value"},
		{"two values", base(&FunctionInvokeConfig{Function: "thumbs", Event: `{} {}`}), "must be one JSON value"},
		{"event too large", base(&FunctionInvokeConfig{Function: "thumbs",
			Event: `"` + strings.Repeat("a", MaxFunctionEventBytes) + `"`}), "at most"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := c.job.Validate()
			if c.want == "" && got != "" || c.want != "" && !strings.Contains(got, c.want) {
				t.Fatalf("Validate() = %q, want %q", got, c.want)
			}
		})
	}
}

func TestJob_Normalize_DefaultEvent(t *testing.T) {
	j := &Job{Name: "n", Schedule: "* * * * *", Type: JobTypeFunctionInvoke, FunctionInvoke: &FunctionInvokeConfig{Function: "thumbs", Event: "  "}}
	j.Normalize()
	if j.FunctionInvoke.Event != "{}" {
		t.Fatalf("event = %q, want {}", j.FunctionInvoke.Event)
	}
	if msg := j.Validate(); msg != "" {
		t.Fatalf("Validate() = %q", msg)
	}
}

func TestFunctionInvoke_MinimumPeriod(t *testing.T) {
	if err := ValidateMinInterval("@every 30s"); err == nil {
		t.Fatal("@every 30s must be refused")
	}
	if err := ValidateMinInterval("@every 1m"); err != nil {
		t.Fatalf("@every 1m: %v", err)
	}
	// Five-field expressions have no seconds field.
	if err := ValidateCronExpression("*/30 * * * * *"); err == nil {
		t.Fatal("a seconds field must be refused")
	}
}

func TestExecutor_FunctionInvoke_Success(t *testing.T) {
	inv := &fakeInvoker{res: &functions.InvokeResult{StatusCode: 200, RequestID: "abc", DurationMs: 12,
		Payload: []byte(`{"secret":"TOP-SECRET-PAYLOAD"}`), LogTail: []byte("LOG-TAIL-LINE")}}
	e, store := newFunctionExecutor(t, inv)
	job := functionJob("j1", "thumbs", "")
	store.Set(job.ID, job)

	e.Run(context.Background(), job)

	got, _ := store.Get("j1")
	if got.LastStatus != JobStatusSuccess || got.LastError != "" {
		t.Fatalf("status %q, error %q", got.LastStatus, got.LastError)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(got.LastOutput), &out); err != nil {
		t.Fatalf("LastOutput is not JSON: %q", got.LastOutput)
	}
	want := map[string]any{"statusCode": 200.0, "durationMs": 12.0, "payloadBytes": float64(len(`{"secret":"TOP-SECRET-PAYLOAD"}`))}
	if len(out) != len(want) {
		t.Fatalf("LastOutput = %v, want %v", out, want)
	}
	for k, v := range want {
		if out[k] != v {
			t.Fatalf("LastOutput[%s] = %v, want %v", k, out[k], v)
		}
	}
	if strings.Contains(got.LastOutput, "TOP-SECRET") || strings.Contains(got.LastOutput, "LOG-TAIL") || strings.Contains(got.LastOutput, "abc") {
		t.Fatalf("LastOutput leaks the payload, log tail or request id: %q", got.LastOutput)
	}
	if len(inv.calls) != 1 {
		t.Fatalf("calls = %d", len(inv.calls))
	}
	c := inv.calls[0]
	if c.name != "thumbs" || c.event != "{}" || c.actor != "cron:j1" {
		t.Fatalf("call = %+v", c)
	}
}

func TestExecutor_FunctionInvoke_FunctionError(t *testing.T) {
	inv := &fakeInvoker{res: &functions.InvokeResult{StatusCode: 200, FunctionError: "Unhandled", DurationMs: 5,
		Payload: []byte(`{"errorMessage":"boom SECRET"}`)}}
	e, store := newFunctionExecutor(t, inv)
	job := functionJob("j1", "thumbs", `{"k":"v"}`)
	store.Set(job.ID, job)

	e.Run(context.Background(), job)

	got, _ := store.Get("j1")
	if got.LastStatus != JobStatusError || !strings.Contains(got.LastError, "Unhandled") {
		t.Fatalf("status %q, error %q", got.LastStatus, got.LastError)
	}
	if !strings.Contains(got.LastOutput, `"functionError":"Unhandled"`) || strings.Contains(got.LastOutput, "SECRET") ||
		strings.Contains(got.LastError, "SECRET") {
		t.Fatalf("LastOutput %q / LastError %q", got.LastOutput, got.LastError)
	}
	if inv.calls[0].event != `{"k":"v"}` {
		t.Fatalf("event = %q", inv.calls[0].event)
	}
}

func TestExecutor_FunctionInvoke_ErrorStatus(t *testing.T) {
	inv := &fakeInvoker{res: &functions.InvokeResult{StatusCode: 502, DurationMs: 1}}
	e, store := newFunctionExecutor(t, inv)
	job := functionJob("j1", "thumbs", "{}")
	store.Set(job.ID, job)
	e.Run(context.Background(), job)
	got, _ := store.Get("j1")
	if got.LastStatus != JobStatusError || !strings.Contains(got.LastError, "502") {
		t.Fatalf("status %q, error %q", got.LastStatus, got.LastError)
	}
}

func TestExecutor_FunctionInvoke_InvokeError(t *testing.T) {
	cases := []struct {
		name   string
		err    error
		want   string
		hidden string
	}{
		{"not found", &functions.Error{Code: functions.CodeNotFound, Message: "function not found"}, "function not found (FUNCTION_NOT_FOUND)", ""},
		{"busy", &functions.Error{Code: functions.CodeInvokeBusy, Message: "too many invocations"}, "FUNCTION_INVOKE_BUSY", ""},
		{"internal", &functions.Error{Code: functions.CodeStorage, Message: "/data/functions/.loch secret path"}, "FUNCTION_STORAGE_ERROR", "/data"},
		{"other", errors.New("dial unix /var/run/docker.sock"), "function invocation failed", "docker.sock"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e, store := newFunctionExecutor(t, &fakeInvoker{err: c.err})
			job := functionJob("j1", "thumbs", "{}")
			store.Set(job.ID, job)
			e.Run(context.Background(), job)
			got, _ := store.Get("j1")
			if got.LastStatus != JobStatusError || !strings.Contains(got.LastError, c.want) {
				t.Fatalf("status %q, error %q", got.LastStatus, got.LastError)
			}
			if c.hidden != "" && strings.Contains(got.LastError, c.hidden) {
				t.Fatalf("LastError leaks %q: %q", c.hidden, got.LastError)
			}
			if got.LastOutput != "" {
				t.Fatalf("LastOutput = %q", got.LastOutput)
			}
		})
	}
}

func TestExecutor_FunctionInvoke_ModuleDisabled(t *testing.T) {
	e, store := newFunctionExecutor(t, nil)
	job := functionJob("j1", "thumbs", "{}")
	store.Set(job.ID, job)
	e.Run(context.Background(), job)
	got, _ := store.Get("j1")
	if got.LastStatus != JobStatusError || !strings.Contains(got.LastError, functions.CodeDisabled) {
		t.Fatalf("status %q, error %q", got.LastStatus, got.LastError)
	}
}

// A run that ends after its job was deleted does not bring it back, and
// one that ends after it was disabled keeps it disabled.
func TestExecutor_RunKeepsConcurrentChanges(t *testing.T) {
	inv := &fakeInvoker{res: &functions.InvokeResult{StatusCode: 200}}
	e, store := newFunctionExecutor(t, inv)

	job := functionJob("gone", "thumbs", "{}")
	e.Run(context.Background(), job) // never stored
	if _, ok := store.Get("gone"); ok {
		t.Fatal("a deleted job was written back")
	}

	job = functionJob("j1", "thumbs", "{}")
	store.Set(job.ID, job)
	store.Update("j1", func(j *Job) { j.Enabled = false; j.DisabledReason = DisabledReasonFunctionDeleted })
	e.Run(context.Background(), job) // job (the runner's copy) is still enabled
	got, _ := store.Get("j1")
	if got.Enabled || got.DisabledReason != DisabledReasonFunctionDeleted || got.LastStatus != JobStatusSuccess {
		t.Fatalf("got enabled=%v reason=%q status=%q", got.Enabled, got.DisabledReason, got.LastStatus)
	}
}

func TestScheduler_FunctionInvoke_NoOverlap(t *testing.T) {
	inv := &fakeInvoker{res: &functions.InvokeResult{StatusCode: 200}, block: make(chan struct{}), started: make(chan struct{}, 1)}
	e, store := newFunctionExecutor(t, inv)
	s := NewScheduler(store, e, testLogger())
	store.Set("j1", functionJob("j1", "thumbs", "{}"))

	done := make(chan struct{})
	go func() {
		s.executeWithLock("j1")
		close(done)
	}()
	<-inv.started

	// The previous invocation still runs: this run is skipped and recorded.
	s.executeWithLock("j1")
	got, _ := store.Get("j1")
	if got.LastSkipped == nil {
		t.Fatal("the skipped run is not recorded")
	}
	if got.LastStatus != JobStatusRunning {
		t.Fatalf("status = %q, want running", got.LastStatus)
	}

	close(inv.block)
	<-done
	if n := inv.callCount(); n != 1 {
		t.Fatalf("invocations = %d, want 1", n)
	}
	got, _ = store.Get("j1")
	if got.LastSkipped == nil || got.LastStatus != JobStatusSuccess {
		t.Fatalf("after the run: skipped=%v status=%q", got.LastSkipped, got.LastStatus)
	}
}

func TestScheduler_DisableFunctionJobs(t *testing.T) {
	e, store := newFunctionExecutor(t, &fakeInvoker{res: &functions.InvokeResult{StatusCode: 200}})
	s := NewScheduler(store, e, testLogger())
	now := time.Now()
	for _, j := range []*Job{
		functionJob("a", "thumbs", "{}"),
		functionJob("b", "thumbs", "{}"),
		functionJob("c", "other", "{}"),
		makeJob("d", "http", true, now),
	} {
		store.Set(j.ID, j)
		if err := s.AddJob(j); err != nil {
			t.Fatal(err)
		}
	}
	b, _ := store.Get("b")
	b.Enabled = false
	store.Set("b", b)

	changed := s.DisableFunctionJobs("thumbs", DisabledReasonFunctionDeleted)
	if len(changed) != 2 {
		t.Fatalf("changed = %d, want 2", len(changed))
	}
	for _, id := range []string{"a", "b"} {
		j, ok := store.Get(id)
		if !ok || j.Enabled || j.DisabledReason != DisabledReasonFunctionDeleted {
			t.Fatalf("%s: kept=%v enabled=%v reason=%q", id, ok, j.Enabled, j.DisabledReason)
		}
	}
	if _, ok := s.entries["a"]; ok {
		t.Fatal("a is still scheduled")
	}
	for _, id := range []string{"c", "d"} {
		j, _ := store.Get(id)
		if !j.Enabled || j.DisabledReason != "" {
			t.Fatalf("%s changed", id)
		}
		if _, ok := s.entries[id]; !ok {
			t.Fatalf("%s unscheduled", id)
		}
	}
}

func TestStore_UpdateMissing(t *testing.T) {
	s := newTestStore(t)
	if s.Update("nope", func(j *Job) { j.Name = "x" }) {
		t.Fatal("Update of a missing job returned true")
	}
	if len(s.List()) != 0 {
		t.Fatal("Update created a job")
	}
}

// existsInvoker is a FunctionInvoker that also answers Exists.
type existsInvoker struct {
	fakeInvoker
	known map[string]bool
}

func (e *existsInvoker) Exists(name string) error {
	if e.known[name] {
		return nil
	}
	return &functions.Error{Code: functions.CodeNotFound, Message: "function not found"}
}

func TestScheduler_FunctionJobsNotScheduledWithoutModule(t *testing.T) {
	e, store := newFunctionExecutor(t, nil)
	s := NewScheduler(store, e, testLogger())
	fj, hj := functionJob("f", "thumbs", "{}"), makeJob("h", "http", true, time.Now())
	store.Set(fj.ID, fj)
	store.Set(hj.ID, hj)
	s.Start()
	defer s.Stop()
	if _, ok := s.entries["f"]; ok {
		t.Fatal("a function_invoke job is scheduled without the module")
	}
	if _, ok := s.entries["h"]; !ok {
		t.Fatal("the http job is not scheduled")
	}
	if err := s.AddJob(fj); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.entries["f"]; ok {
		t.Fatal("AddJob scheduled it")
	}
	if j, _ := store.Get("f"); !j.Enabled || j.DisabledReason != "" {
		t.Fatal("the job itself must be kept as it is")
	}
}

func TestScheduler_StartDisablesJobsOfMissingFunctions(t *testing.T) {
	inv := &existsInvoker{fakeInvoker: fakeInvoker{res: &functions.InvokeResult{StatusCode: 200}}, known: map[string]bool{"thumbs": true}}
	e, store := newFunctionExecutor(t, inv)
	s := NewScheduler(store, e, testLogger())
	store.Set("a", functionJob("a", "thumbs", "{}"))
	store.Set("b", functionJob("b", "gone", "{}"))
	s.Start()
	defer s.Stop()
	if _, ok := s.entries["a"]; !ok {
		t.Fatal("a is not scheduled")
	}
	b, _ := store.Get("b")
	if _, ok := s.entries["b"]; ok || b.Enabled || b.DisabledReason != DisabledReasonFunctionDeleted {
		t.Fatalf("b: scheduled=%v enabled=%v reason=%q", ok, b.Enabled, b.DisabledReason)
	}
}

// Once is decided on the live job: a job made recurring while it ran
// stays enabled and scheduled.
func TestScheduler_OnceDecidedOnLiveJob(t *testing.T) {
	inv := &fakeInvoker{res: &functions.InvokeResult{StatusCode: 200}, block: make(chan struct{}), started: make(chan struct{}, 1)}
	e, store := newFunctionExecutor(t, inv)
	s := NewScheduler(store, e, testLogger())
	j := functionJob("j1", "thumbs", "{}")
	j.Once = true
	store.Set(j.ID, j)
	if err := s.AddJob(j); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { s.executeWithLock("j1"); close(done) }()
	<-inv.started
	store.Update("j1", func(j *Job) { j.Once = false })
	close(inv.block)
	<-done
	got, _ := store.Get("j1")
	if !got.Enabled {
		t.Fatal("a job no longer once was disabled")
	}
	s.mu.Lock()
	_, scheduled := s.entries["j1"]
	s.mu.Unlock()
	if !scheduled {
		t.Fatal("it was unscheduled")
	}
}

func TestJob_NormalizeKeepsOneConfigAndCopies(t *testing.T) {
	shared := &FunctionInvokeConfig{Function: "thumbs"}
	j := &Job{Type: JobTypeFunctionInvoke, FunctionInvoke: shared, HTTP: &HTTPConfig{Method: "GET", URL: "https://x"}}
	j.Normalize()
	if shared.Event != "" || j.FunctionInvoke == shared || j.FunctionInvoke.Event != "{}" {
		t.Fatal("Normalize wrote through the shared pointer")
	}
	if j.HTTP != nil {
		t.Fatal("the http block of a function_invoke job is kept")
	}
	j = &Job{Type: JobTypeHTTP, HTTP: &HTTPConfig{Method: "GET", URL: "https://x"}, FunctionInvoke: shared}
	j.Normalize()
	if j.FunctionInvoke != nil || j.HTTP == nil {
		t.Fatal("type switch kept the functionInvoke block")
	}
}
