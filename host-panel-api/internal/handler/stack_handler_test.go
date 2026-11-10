package handler

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"

	"host-panel-api/internal/model"

	"github.com/moby/moby/api/types/container"
)

func stackContainer(id, stack, service, dependsOn string, state container.ContainerState) container.Summary {
	labels := map[string]string{}
	if stack != "" {
		labels[composeProjectLabel] = stack
	}
	if service != "" {
		labels[composeServiceLabel] = service
	}
	if dependsOn != "" {
		labels[composeDependsOnLabel] = dependsOn
	}
	return container.Summary{
		ID:     id,
		Names:  []string{"/" + stack + "-" + service + "-1"},
		Labels: labels,
		State:  state,
	}
}

func serviceNames(level []container.Summary) []string {
	names := make([]string, 0, len(level))
	for _, c := range level {
		names = append(names, c.Labels[composeServiceLabel])
	}
	return names
}

func TestParseDependsOn(t *testing.T) {
	tests := []struct {
		label string
		want  []string
	}{
		{"", nil},
		{"db:service_started:false", []string{"db"}},
		{"db:service_healthy:true,redis:service_started:false", []string{"db", "redis"}},
		{"db, redis", []string{"db", "redis"}},
		{",,", nil},
	}
	for _, tt := range tests {
		if got := parseDependsOn(tt.label); !slices.Equal(got, tt.want) {
			t.Errorf("parseDependsOn(%q) = %v, want %v", tt.label, got, tt.want)
		}
	}
}

func TestSelectStackContainers(t *testing.T) {
	system := stackContainer("sys", "app", "traefik", "", container.StateRunning)
	system.Labels["system"] = "true"

	containers := []container.Summary{
		stackContainer("aaa111", "app", "web", "", container.StateRunning),
		stackContainer("bbb222", "app", "db", "", container.StateRunning),
		stackContainer("ccc333", "other", "web", "", container.StateRunning),
		stackContainer("ddd444", "", "", "", container.StateRunning),
		stackContainer("self123456789", "app", "panel", "", container.StateRunning),
		system,
	}

	admin := &model.User{Profile: model.ProfileAdmin}
	got := selectStackContainers(containers, "app", admin, "self12345678")
	if ids := idsOf(got); !slices.Equal(ids, []string{"aaa111", "bbb222"}) {
		t.Errorf("admin selection = %v, want [aaa111 bbb222]", ids)
	}

	restricted := &model.User{Profile: model.ProfileRestricted, Permissions: []string{"app-db-1"}}
	got = selectStackContainers(containers, "app", restricted, "")
	if ids := idsOf(got); !slices.Equal(ids, []string{"bbb222"}) {
		t.Errorf("restricted selection = %v, want [bbb222]", ids)
	}

	if got := selectStackContainers(containers, "missing", admin, ""); len(got) != 0 {
		t.Errorf("unknown stack should select nothing, got %d", len(got))
	}
}

func idsOf(containers []container.Summary) []string {
	ids := make([]string, 0, len(containers))
	for _, c := range containers {
		ids = append(ids, c.ID)
	}
	return ids
}

func TestOrderByDependencies(t *testing.T) {
	containers := []container.Summary{
		stackContainer("1", "app", "web", "api:service_started:false", container.StateExited),
		stackContainer("2", "app", "api", "db:service_healthy:true,cache:service_started:false", container.StateExited),
		stackContainer("3", "app", "db", "", container.StateExited),
		stackContainer("4", "app", "cache", "", container.StateExited),
		stackContainer("5", "app", "worker", "db:service_started:false,external:service_started:false", container.StateExited),
	}

	levels := orderByDependencies(containers)
	want := [][]string{{"cache", "db"}, {"api", "worker"}, {"web"}}
	if len(levels) != len(want) {
		t.Fatalf("got %d levels, want %d", len(levels), len(want))
	}
	for i := range want {
		if got := serviceNames(levels[i]); !slices.Equal(got, want[i]) {
			t.Errorf("level %d = %v, want %v", i, got, want[i])
		}
	}

	reverseLevels(levels)
	if got := serviceNames(levels[0]); !slices.Equal(got, []string{"web"}) {
		t.Errorf("reversed first level = %v, want [web]", got)
	}
}

func TestOrderByDependencies_Cycle(t *testing.T) {
	containers := []container.Summary{
		stackContainer("1", "app", "a", "b:service_started:false", container.StateExited),
		stackContainer("2", "app", "b", "a:service_started:false", container.StateExited),
		stackContainer("3", "app", "c", "", container.StateExited),
	}

	levels := orderByDependencies(containers)
	if len(levels) != 2 {
		t.Fatalf("got %d levels, want 2", len(levels))
	}
	if got := serviceNames(levels[1]); !slices.Equal(got, []string{"a", "b"}) {
		t.Errorf("cycle level = %v, want [a b]", got)
	}
}

func TestOrderByDependencies_Replicas(t *testing.T) {
	containers := []container.Summary{
		stackContainer("1", "app", "worker", "db", container.StateExited),
		stackContainer("2", "app", "worker", "db", container.StateExited),
		stackContainer("3", "app", "db", "", container.StateExited),
	}
	levels := orderByDependencies(containers)
	if len(levels) != 2 || len(levels[1]) != 2 {
		t.Fatalf("expected db then both worker replicas, got %v", levels)
	}
}

func TestRunLevels(t *testing.T) {
	levels := [][]container.Summary{
		{
			stackContainer("db", "app", "db", "", container.StateRunning),
			stackContainer("cache", "app", "cache", "", container.StateExited),
		},
		{stackContainer("api", "app", "api", "", container.StateExited)},
	}

	var mu sync.Mutex
	var called []string
	run := func(_ context.Context, id string) error {
		mu.Lock()
		called = append(called, id)
		mu.Unlock()
		if id == "api" {
			return errors.New("boom")
		}
		return nil
	}

	results := runLevels(context.Background(), levels, "start", run)

	// "db" is already running: skipped for start.
	slices.Sort(called)
	if !slices.Equal(called, []string{"api", "cache"}) {
		t.Errorf("called = %v, want [api cache]", called)
	}
	if len(results) != 3 {
		t.Fatalf("got %d results, want 3", len(results))
	}
	for _, r := range results {
		wantOK := r.ID != "api"
		if r.OK != wantOK {
			t.Errorf("result %s ok = %v, want %v", r.ID, r.OK, wantOK)
		}
	}
	if results[2].Error == "" || results[2].Error == "boom" {
		t.Errorf("failed result should carry a generic error, got %q", results[2].Error)
	}
}

func TestSkipAction(t *testing.T) {
	tests := []struct {
		action string
		state  container.ContainerState
		want   bool
	}{
		{"start", container.StateRunning, true},
		{"start", container.StateExited, false},
		{"stop", container.StateExited, true},
		{"stop", container.StateCreated, true},
		{"stop", container.StateRunning, false},
		{"stop", container.StatePaused, false},
		{"restart", container.StateExited, false},
		{"restart", container.StateRunning, false},
	}
	for _, tt := range tests {
		if got := skipAction(tt.action, tt.state); got != tt.want {
			t.Errorf("skipAction(%s, %s) = %v, want %v", tt.action, tt.state, got, tt.want)
		}
	}
}

func TestValidStackName(t *testing.T) {
	for _, name := range []string{"app", "my-stack_2", "prod.web"} {
		if !validStackName(name) {
			t.Errorf("%q should be valid", name)
		}
	}
	for _, name := range []string{"", "bad\nname", "nul\x00", string(make([]byte, 256))} {
		if validStackName(name) {
			t.Errorf("%q should be invalid", name)
		}
	}
}
