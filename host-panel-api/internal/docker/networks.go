package docker

import (
	"context"
	"errors"
	"net/netip"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"
)

// ErrNetworkNotFound is returned by InspectNetwork for a missing network.
var ErrNetworkNotFound = errors.New("network not found")

// InspectNetwork returns a network by exact name (NetworkInspect also
// resolves ID prefixes: a network whose name differs is reported missing).
func (c *Client) InspectNetwork(ctx context.Context, name string) (network.Inspect, error) {
	res, err := c.cli.NetworkInspect(ctx, name, client.NetworkInspectOptions{})
	if err != nil {
		if cerrdefs.IsNotFound(err) {
			return network.Inspect{}, ErrNetworkNotFound
		}
		return network.Inspect{}, err
	}
	if res.Network.Name != name {
		return network.Inspect{}, ErrNetworkNotFound
	}
	return res.Network, nil
}

// CreateInternalNetwork creates an internal bridge network (no outbound
// access, IPv4 only). A zero subnet lets Docker allocate one from its
// address pools (daemon setting default-address-pools).
func (c *Client) CreateInternalNetwork(ctx context.Context, name string, subnet netip.Prefix, labels map[string]string) (string, error) {
	no := false
	opts := client.NetworkCreateOptions{Driver: "bridge", Internal: true, EnableIPv6: &no, Labels: labels}
	if subnet.IsValid() {
		opts.IPAM = &network.IPAM{Driver: "default", Config: []network.IPAMConfig{{Subnet: subnet}}}
	}
	res, err := c.cli.NetworkCreate(ctx, name, opts)
	if err != nil {
		return "", err
	}
	return res.ID, nil
}

// RemoveNetworkByID removes a network by ID (a missing one is not an error).
func (c *Client) RemoveNetworkByID(ctx context.Context, id string) error {
	_, err := c.cli.NetworkRemove(ctx, id, client.NetworkRemoveOptions{})
	if err != nil && cerrdefs.IsNotFound(err) {
		return nil
	}
	return err
}

// DisconnectNetwork disconnects a container from a network (forced: the
// container may be stopped). A container already gone is not an error.
func (c *Client) DisconnectNetwork(ctx context.Context, networkID, containerID string) error {
	_, err := c.cli.NetworkDisconnect(ctx, networkID, client.NetworkDisconnectOptions{Container: containerID, Force: true})
	if err != nil && cerrdefs.IsNotFound(err) {
		return nil
	}
	return err
}

// ListNetworks returns every Docker network of the host.
func (c *Client) ListNetworks(ctx context.Context) ([]network.Summary, error) {
	res, err := c.cli.NetworkList(ctx, client.NetworkListOptions{})
	if err != nil {
		return nil, err
	}
	return res.Items, nil
}

// InspectNetworkByID returns a network by its full ID (a prefix or a name
// is reported missing: NetworkInspect resolves both).
func (c *Client) InspectNetworkByID(ctx context.Context, id string) (network.Inspect, error) {
	res, err := c.cli.NetworkInspect(ctx, id, client.NetworkInspectOptions{})
	if err != nil {
		if cerrdefs.IsNotFound(err) {
			return network.Inspect{}, ErrNetworkNotFound
		}
		return network.Inspect{}, err
	}
	if res.Network.ID != id {
		return network.Inspect{}, ErrNetworkNotFound
	}
	return res.Network, nil
}
