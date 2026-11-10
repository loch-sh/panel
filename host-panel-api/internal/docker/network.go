package docker

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"
)

// NetworkBridge manages dynamic Docker network connections with reference counting.
// When the API container needs to reach a container on a different network,
// it temporarily joins that network and disconnects when no longer needed.
type NetworkBridge struct {
	mu     sync.Mutex
	wg     sync.WaitGroup
	cli    *client.Client
	selfID string
	refs   map[string]int // networkID -> reference count
	logger *slog.Logger

	// onChanged is called after a successful connect or disconnect
	// to let the caller refresh any cached state (e.g. self network cache).
	onChanged func()

	// Hooks for testing. When nil, the real Docker client is used.
	connectFn    func(ctx context.Context, networkID, containerID string) error
	disconnectFn func(networkID, containerID string) error
}

// NewNetworkBridge creates a new NetworkBridge.
// onChanged is called after each successful connect/disconnect (may be nil).
func NewNetworkBridge(cli *client.Client, selfID string, logger *slog.Logger, onChanged func()) *NetworkBridge {
	return &NetworkBridge{
		cli:       cli,
		selfID:    selfID,
		refs:      make(map[string]int),
		logger:    logger,
		onChanged: onChanged,
	}
}

// Acquire connects the API container to the given network if not already connected.
// Multiple callers can acquire the same network; the connection is maintained
// until all callers have released it.
func (b *NetworkBridge) Acquire(ctx context.Context, networkID string) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.refs[networkID] > 0 {
		b.refs[networkID]++
		b.logger.Debug("network bridge ref incremented", "network", networkID, "refs", b.refs[networkID])
		return nil
	}

	if err := b.doConnect(ctx, networkID); err != nil {
		return err
	}

	b.refs[networkID] = 1
	b.logger.Info("network bridge connected", "network", networkID)
	if b.onChanged != nil {
		b.onChanged()
	}
	return nil
}

// Release decrements the reference count for a network and disconnects
// when the count reaches zero. The disconnect runs in a background goroutine
// with its own context since the caller's context may already be cancelled.
func (b *NetworkBridge) Release(networkID string) {
	b.mu.Lock()
	defer b.mu.Unlock()

	count, ok := b.refs[networkID]
	if !ok {
		return
	}

	count--
	if count > 0 {
		b.refs[networkID] = count
		b.logger.Debug("network bridge ref decremented", "network", networkID, "refs", count)
		return
	}

	delete(b.refs, networkID)
	b.wg.Add(1)
	go func() {
		defer b.wg.Done()
		b.doDisconnect(networkID)
	}()
}

// Close disconnects from all remaining networks. Called during graceful shutdown.
func (b *NetworkBridge) Close() {
	b.mu.Lock()
	networks := make([]string, 0, len(b.refs))
	for id := range b.refs {
		networks = append(networks, id)
	}
	b.refs = make(map[string]int)
	b.mu.Unlock()

	// Wait for in-flight Release goroutines before issuing our own disconnects,
	// to avoid double-disconnect on the same network.
	b.wg.Wait()

	for _, id := range networks {
		b.doDisconnect(id)
	}
}

func (b *NetworkBridge) doConnect(ctx context.Context, networkID string) error {
	if b.connectFn != nil {
		return b.connectFn(ctx, networkID, b.selfID)
	}
	_, err := b.cli.NetworkConnect(ctx, networkID, client.NetworkConnectOptions{
		Container:      b.selfID,
		EndpointConfig: &network.EndpointSettings{},
	})
	return err
}

func (b *NetworkBridge) doDisconnect(networkID string) {
	var err error
	if b.disconnectFn != nil {
		err = b.disconnectFn(networkID, b.selfID)
	} else {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_, err = b.cli.NetworkDisconnect(ctx, networkID, client.NetworkDisconnectOptions{Container: b.selfID})
	}
	if err != nil {
		b.logger.Warn("network bridge disconnect failed", "network", networkID, "error", err)
		return
	}
	b.logger.Info("network bridge disconnected", "network", networkID)
	if b.onChanged != nil {
		b.onChanged()
	}
}
