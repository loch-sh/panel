package main

import (
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"host-panel-api/internal/config"
	"host-panel-api/internal/services"
)

func engineIDs(t *testing.T, cfg *config.Config) ([]string, []string, map[string][]string, bool, string) {
	t.Helper()
	logs := &strings.Builder{}
	engines, enabled, versions, ok := selectServiceEngines(cfg, slog.New(slog.NewTextHandler(logs, nil)))
	var ids []string
	for _, e := range engines {
		ids = append(ids, e.ID())
	}
	return ids, enabled, versions, ok, logs.String()
}

func TestSelectServiceEngines(t *testing.T) {
	stacks := t.TempDir()
	if err := os.Mkdir(filepath.Join(stacks, "svc-s3-media"), 0o755); err != nil {
		t.Fatal(err)
	}

	// No key: standalone mode, everything as configured.
	cfg := &config.Config{ServicesEngines: []string{"s3", "redis"}, ServicesPinnedVersions: map[string][]string{"s3": {"1.4"}}, HostingStacksDir: stacks}
	ids, enabled, versions, ok, _ := engineIDs(t, cfg)
	if !ok || !slices.Contains(ids, "s3") || !slices.Equal(enabled, cfg.ServicesEngines) || versions["s3"] == nil {
		t.Fatalf("%v %v %v %v", ids, enabled, versions, ok)
	}

	// Key set: s3 removed everywhere, the configuration left untouched.
	cfg.ServicesS3AdminAPIKey = "x"
	ids, enabled, versions, ok, logs := engineIDs(t, cfg)
	if !ok || slices.Contains(ids, "s3") || !slices.Equal(enabled, []string{"redis"}) || versions["s3"] != nil {
		t.Fatalf("%v %v %v %v", ids, enabled, versions, ok)
	}
	if !slices.Equal(cfg.ServicesEngines, []string{"s3", "redis"}) || cfg.ServicesPinnedVersions["s3"] == nil {
		t.Fatal("the configuration must not be modified")
	}
	if !strings.Contains(logs, "svc-s3-media") || !strings.Contains(logs, "not available yet") {
		t.Fatalf("the unmanaged instances are listed: %s", logs)
	}

	// Only s3 enabled: nothing left, the module is disabled.
	cfg.ServicesEngines = []string{"s3"}
	if _, _, _, ok, _ := engineIDs(t, cfg); ok {
		t.Fatal("no engine left")
	}
	// All engines (empty list): everything but s3.
	cfg.ServicesEngines = nil
	if ids, enabled, _, ok, _ := engineIDs(t, cfg); !ok || enabled != nil || slices.Contains(ids, "s3") || len(ids) == 0 {
		t.Fatalf("%v %v", ids, enabled)
	}
}

// Every engine definition passes the registry checks (names, files, keys).
func TestServiceEnginesRegister(t *testing.T) {
	if _, err := services.NewRegistry(serviceEngines(), nil, nil, nil); err != nil {
		t.Fatal(err)
	}
}
