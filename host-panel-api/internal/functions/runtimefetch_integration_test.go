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

// TestRuntimeRealDownload downloads the newest release of the embedded
// trust table from GitHub, for both architectures, and installs it:
//
//	LOCH_E2E_DOWNLOAD=1 go test -tags integration -run RealDownload -count=1 -v ./internal/functions/
func TestRuntimeRealDownload(t *testing.T) {
	if os.Getenv("LOCH_E2E_DOWNLOAD") != "1" {
		t.Skip("set LOCH_E2E_DOWNLOAD=1 to download the runtime from GitHub")
	}
	trust, err := EmbeddedTrust()
	if err != nil {
		t.Fatal(err)
	}
	for _, arch := range []string{"amd64", "arm64"} {
		t.Run(arch, func(t *testing.T) {
			store := NewRuntimeStore(Paths{DataDir: t.TempDir()}, trust)
			audit := &bytes.Buffer{}
			p := NewRuntimeProvider(store, arch, NewRuntimeDownloader(),
				slog.New(slog.NewTextHandler(os.Stderr, nil)), slog.New(slog.NewJSONHandler(audit, nil)))
			p.Backoff = nil
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			defer cancel()
			p.Run(ctx)
			newest, _ := trust.Newest(arch)
			b, err := p.Latest(arch)
			if err != nil || b.Version != newest.Version || b.SHA256 != newest.SHA256 {
				t.Fatalf("Latest = %+v, %v", b, err)
			}
			if !strings.Contains(audit.String(), `"source":"download"`) {
				t.Fatalf("audit = %s", audit)
			}
			t.Logf("%s %s installed (%s)", b.Version, arch, b.SHA256)
		})
	}
}
