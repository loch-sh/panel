package docker

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"testing"
)

// testBridge creates a NetworkBridge with overridable connect/disconnect hooks
// to test ref-counting without a real Docker daemon.
type testHooks struct {
	mu              sync.Mutex
	connectCalls    map[string]int
	disconnectCalls map[string]int
	connectErr      error
}

func newTestHooks() *testHooks {
	return &testHooks{
		connectCalls:    make(map[string]int),
		disconnectCalls: make(map[string]int),
	}
}

func (h *testHooks) connect(_ context.Context, networkID, _ string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.connectErr != nil {
		return h.connectErr
	}
	h.connectCalls[networkID]++
	return nil
}

func (h *testHooks) disconnect(networkID, _ string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.disconnectCalls[networkID]++
	return nil
}

func newTestBridge(hooks *testHooks) *NetworkBridge {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	b := &NetworkBridge{
		selfID: "self-abc123",
		refs:   make(map[string]int),
		logger: logger,
	}
	// Override internal methods via the hooks
	b.connectFn = hooks.connect
	b.disconnectFn = hooks.disconnect
	return b
}

func TestNetworkBridge_AcquireRelease(t *testing.T) {
	hooks := newTestHooks()
	b := newTestBridge(hooks)

	ctx := context.Background()

	// First acquire should call connect
	if err := b.Acquire(ctx, "net1"); err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if hooks.connectCalls["net1"] != 1 {
		t.Fatalf("expected 1 connect call, got %d", hooks.connectCalls["net1"])
	}

	// Second acquire should NOT call connect (ref increment)
	if err := b.Acquire(ctx, "net1"); err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if hooks.connectCalls["net1"] != 1 {
		t.Fatalf("expected still 1 connect call, got %d", hooks.connectCalls["net1"])
	}

	// First release should NOT disconnect (ref > 0)
	b.Release("net1")
	if hooks.disconnectCalls["net1"] != 0 {
		t.Fatalf("expected 0 disconnect calls, got %d", hooks.disconnectCalls["net1"])
	}

	// Second release should disconnect (ref reaches 0)
	b.Release("net1")
	// disconnect runs in a goroutine, wait a bit
	b.wg.Wait()
	if hooks.disconnectCalls["net1"] != 1 {
		t.Fatalf("expected 1 disconnect call, got %d", hooks.disconnectCalls["net1"])
	}
}

func TestNetworkBridge_AcquireError(t *testing.T) {
	hooks := newTestHooks()
	hooks.connectErr = fmt.Errorf("connection refused")
	b := newTestBridge(hooks)

	err := b.Acquire(context.Background(), "net1")
	if err == nil {
		t.Fatal("expected error from Acquire")
	}

	// refs should not be set
	b.mu.Lock()
	if _, ok := b.refs["net1"]; ok {
		t.Fatal("refs should not contain net1 after failed acquire")
	}
	b.mu.Unlock()
}

func TestNetworkBridge_MultipleNetworks(t *testing.T) {
	hooks := newTestHooks()
	b := newTestBridge(hooks)
	ctx := context.Background()

	if err := b.Acquire(ctx, "net1"); err != nil {
		t.Fatalf("Acquire net1: %v", err)
	}
	if err := b.Acquire(ctx, "net2"); err != nil {
		t.Fatalf("Acquire net2: %v", err)
	}

	if hooks.connectCalls["net1"] != 1 || hooks.connectCalls["net2"] != 1 {
		t.Fatal("expected 1 connect call per network")
	}

	b.Release("net1")
	b.wg.Wait()
	if hooks.disconnectCalls["net1"] != 1 {
		t.Fatalf("expected net1 disconnected")
	}
	if hooks.disconnectCalls["net2"] != 0 {
		t.Fatalf("expected net2 still connected")
	}

	b.Release("net2")
	b.wg.Wait()
	if hooks.disconnectCalls["net2"] != 1 {
		t.Fatalf("expected net2 disconnected")
	}
}

func TestNetworkBridge_Close(t *testing.T) {
	hooks := newTestHooks()
	b := newTestBridge(hooks)
	ctx := context.Background()

	if err := b.Acquire(ctx, "net1"); err != nil {
		t.Fatalf("Acquire net1: %v", err)
	}
	if err := b.Acquire(ctx, "net2"); err != nil {
		t.Fatalf("Acquire net2: %v", err)
	}
	if err := b.Acquire(ctx, "net1"); err != nil {
		t.Fatalf("Acquire net1 (2nd): %v", err)
	} // ref=2

	b.Close()

	if hooks.disconnectCalls["net1"] != 1 {
		t.Fatalf("expected net1 disconnected on close, got %d", hooks.disconnectCalls["net1"])
	}
	if hooks.disconnectCalls["net2"] != 1 {
		t.Fatalf("expected net2 disconnected on close, got %d", hooks.disconnectCalls["net2"])
	}
}

func TestNetworkBridge_ReleaseUnknown(t *testing.T) {
	hooks := newTestHooks()
	b := newTestBridge(hooks)

	// Should not panic
	b.Release("unknown")
	if hooks.disconnectCalls["unknown"] != 0 {
		t.Fatal("should not disconnect unknown network")
	}
}
