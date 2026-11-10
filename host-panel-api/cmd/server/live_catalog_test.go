//go:build livecatalog

package main

// Manual check of the version discovery against the real registries and
// endoflife.date: go test -tags livecatalog -run TestLiveCatalog -v ./cmd/server/

import (
	"context"
	"fmt"
	"testing"

	"host-panel-api/internal/releases"
	"host-panel-api/internal/services"
)

func TestLiveCatalog(t *testing.T) {
	reg, err := services.NewRegistry(serviceEngines(), nil, map[string][]string{"mysql": {"5.6"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	status, errs := reg.Refresh(context.Background(), releases.New(), nil)
	for _, e := range errs {
		t.Errorf("discovery: %v", e)
	}
	t.Logf("failed: %v", status.Failed)
	for _, e := range reg.Engines() {
		line := e.ID() + ":"
		for _, v := range reg.Versions(e.ID()) {
			line += fmt.Sprintf(" %s[%s active=%s security=%s deprecated=%v pinned=%v]", v.Version, v.Image, v.ActiveSupportUntil, v.SupportUntil, v.Deprecated, v.Pinned)
		}
		t.Log(line)
		if len(reg.Versions(e.ID())) == 0 {
			t.Errorf("%s: nothing offered", e.ID())
		}
	}
}
