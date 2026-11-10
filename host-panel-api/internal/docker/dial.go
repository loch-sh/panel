package docker

import (
	"context"
	"fmt"
	"maps"
	"net"
	"slices"
	"time"

	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"
)

// nopRelease is a no-op release function for when no network bridging is needed.
func nopRelease() {}

// DialContainer opens a TCP connection to a container's internal IP on the given port.
// This works for any port, whether it is published (exposed) or not.
//
// If the API container does not share a network with the target, it dynamically
// joins one of the target's networks via NetworkBridge. The returned release
// function must be called when the connection is no longer needed so the
// network can be left once all streams are done.
func (c *Client) DialContainer(ctx context.Context, containerID string, port uint16) (net.Conn, func(), error) {
	c.resolveSelf(ctx)

	res, err := c.cli.ContainerInspect(ctx, containerID, client.ContainerInspectOptions{})
	if err != nil {
		return nil, nil, fmt.Errorf("inspect container: %w", err)
	}
	info := res.Container

	ip, release, err := c.pickTargetIP(ctx, info.NetworkSettings.Networks)
	if err != nil {
		return nil, nil, err
	}

	addr := fmt.Sprintf("%s:%d", ip, port)
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		release()
		return nil, nil, fmt.Errorf("dial %s: %w", addr, err)
	}
	return conn, release, nil
}

// pickTargetIP selects the best IP to dial on the target container.
// It prefers a network shared with the API container. If none exists and
// network bridging is available, it joins one of the target's networks.
func (c *Client) pickTargetIP(ctx context.Context, targetNetworks map[string]*network.EndpointSettings) (string, func(), error) {
	if len(targetNetworks) == 0 {
		return "", nil, fmt.Errorf("container has no network")
	}

	// If not running in Docker, fall back to first available IP (original behaviour).
	if c.selfID == "" {
		for _, nw := range targetNetworks {
			if nw.IPAddress.IsValid() {
				return nw.IPAddress.String(), nopRelease, nil
			}
		}
		return "", nil, fmt.Errorf("container has no reachable IP address")
	}

	// Use cached self networks to find a shared network. The networks are
	// tried in name order: the choice is deterministic.
	selfNets := c.SelfNetworks()
	names := slices.Sorted(maps.Keys(targetNetworks))

	for _, netName := range names {
		targetNw := targetNetworks[netName]
		if targetNw == nil || !targetNw.IPAddress.IsValid() {
			continue
		}
		if selfNets[netName] {
			return targetNw.IPAddress.String(), nopRelease, nil
		}
	}

	// No shared network — bridge to the first target network with an IP.
	for _, netName := range names {
		targetNw := targetNetworks[netName]
		if targetNw == nil || !targetNw.IPAddress.IsValid() {
			continue
		}
		if err := c.netBridge.Acquire(ctx, netName); err != nil {
			return "", nil, fmt.Errorf("network bridge to %s: %w", netName, err)
		}
		release := func() { c.netBridge.Release(netName) }
		return targetNw.IPAddress.String(), release, nil
	}

	return "", nil, fmt.Errorf("container has no reachable IP address")
}
