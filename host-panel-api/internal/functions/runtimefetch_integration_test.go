//go:build integration

package functions

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"
)

// TestRuntimeRealDownload finds the newest release on GitHub and installs
// it for both architectures, as the panel does:
//
//	LOCH_E2E_DOWNLOAD=1 go test -tags integration -run RealDownload -count=1 -v ./internal/functions/
func TestRuntimeRealDownload(t *testing.T) {
	if os.Getenv("LOCH_E2E_DOWNLOAD") != "1" {
		t.Skip("set LOCH_E2E_DOWNLOAD=1 to download the runtime from GitHub")
	}
	for _, arch := range []string{"amd64", "arm64"} {
		t.Run(arch, func(t *testing.T) {
			paths := Paths{DataDir: t.TempDir()}
			store := NewRuntimeStore(paths, TrustTable{})
			audit := &bytes.Buffer{}
			p := NewRuntimeProvider(store, arch, NewRuntimeDownloader(),
				slog.New(slog.NewTextHandler(os.Stderr, nil)), slog.New(slog.NewJSONHandler(audit, nil)))
			p.Backoff, p.CheckInterval = nil, 0
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			defer cancel()
			p.Run(ctx)
			b, err := p.Latest(arch)
			if err != nil || compareRuntimeVersions(b.Version, MinRuntimeVersion) < 0 {
				t.Fatalf("Latest = %+v, %v", b, err)
			}
			if rec, err := LoadRuntimeRecord(paths); err != nil || len(rec.Releases) == 0 {
				t.Fatalf("record = %+v, %v", rec, err)
			}
			if !strings.Contains(audit.String(), `"source":"download"`) {
				t.Fatalf("audit = %s", audit)
			}
			t.Logf("%s %s installed (%s)", b.Version, arch, b.SHA256)
		})
	}
}
