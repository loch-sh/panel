package docker

import (
	"context"
	"fmt"
	"github.com/moby/moby/api/types/image"
	"io"
	"log/slog"
	"os"
	"sync"
	"time"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"
)

// Client wraps the Docker SDK client.
type Client struct {
	cli       *client.Client
	logger    *slog.Logger
	selfID    string // container ID of this process (empty if not in Docker)
	selfOnce  sync.Once
	netBridge *NetworkBridge

	selfNetMu    sync.RWMutex
	selfNetCache map[string]bool // cached set of network names for self container
}

// NewClient creates a Docker client using environment variables
// or the default Unix socket (/var/run/docker.sock).
func NewClient(logger *slog.Logger) (*Client, error) {
	cli, err := client.New(client.FromEnv)
	if err != nil {
		return nil, err
	}
	return &Client{cli: cli, logger: logger}, nil
}

// resolveSelf detects whether this process runs inside a Docker container.
// If so, it sets selfID and initialises the NetworkBridge.
// Uses a background context with timeout so that a cancelled caller context
// cannot permanently disable network bridging via sync.Once.
func (c *Client) resolveSelf(_ context.Context) {
	c.selfOnce.Do(func() {
		hostname, err := os.Hostname()
		if err != nil {
			c.logger.Debug("could not resolve hostname, network bridging disabled", "error", err)
			return
		}
		// Docker sets hostname = container ID by default.
		// Validate by inspecting the container.
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := c.cli.ContainerInspect(ctx, hostname, client.ContainerInspectOptions{}); err != nil {
			c.logger.Debug("not running inside Docker, network bridging disabled")
			return
		}
		c.selfID = hostname
		c.netBridge = NewNetworkBridge(c.cli, hostname, c.logger, c.invalidateSelfNetCache)
		c.refreshSelfNetCache(context.Background())
		c.logger.Info("detected own container", "id", hostname)
	})
}

// SelfID returns the container ID of this process, or an empty string when
// the API does not run inside Docker.
func (c *Client) SelfID() string {
	c.resolveSelf(context.Background())
	return c.selfID
}

// InspectImage returns the configuration of an image (by ID or reference).
func (c *Client) InspectImage(ctx context.Context, ref string) (image.InspectResponse, error) {
	res, err := c.cli.ImageInspect(ctx, ref)
	return res.InspectResponse, err
}

// IsSelf reports whether ref (id, short id or name) is the container of this
// process. Opening a shell in it, copying files to or from it, following
// its files or running commands in it would give access to the audit log:
// these operations refuse it. A failed lookup reports false (the operation
// then fails on its own).
func (c *Client) IsSelf(ctx context.Context, ref string) bool {
	self := c.SelfID()
	if self == "" || ref == "" {
		return false
	}
	target, err := c.cli.ContainerInspect(ctx, ref, client.ContainerInspectOptions{})
	if err != nil {
		return false
	}
	own, err := c.cli.ContainerInspect(ctx, self, client.ContainerInspectOptions{})
	return err == nil && target.Container.ID == own.Container.ID
}

// Ping checks the connection to the Docker daemon.
func (c *Client) Ping(ctx context.Context) (bool, error) {
	_, err := c.cli.Ping(ctx, client.PingOptions{})
	return err == nil, err
}

// HostResources returns the number of CPUs and the total memory (bytes)
// available to the Docker daemon.
func (c *Client) HostResources(ctx context.Context) (int, int64, error) {
	res, err := c.cli.Info(ctx, client.InfoOptions{})
	if err != nil {
		return 0, 0, err
	}
	return res.Info.NCPU, res.Info.MemTotal, nil
}

// ListContainers returns all containers (including stopped ones).
func (c *Client) ListContainers(ctx context.Context) ([]container.Summary, error) {
	res, err := c.cli.ContainerList(ctx, client.ContainerListOptions{All: true})
	if err != nil {
		return nil, err
	}
	return res.Items, nil
}

// InspectContainer returns the details of a container. This is the inspect
// path of every client-facing view (GET /docker/{id}, the WebSocket, the
// MCP tools through the API): the environment values of function
// containers are masked here, centrally (MaskFunctionEnv). The panel's own
// verifications of a function use InspectContainerRaw.
func (c *Client) InspectContainer(ctx context.Context, id string) (container.InspectResponse, error) {
	res, err := c.cli.ContainerInspect(ctx, id, client.ContainerInspectOptions{})
	return MaskFunctionEnv(res.Container), err
}

// StartContainer starts a container.
func (c *Client) StartContainer(ctx context.Context, id string) error {
	_, err := c.cli.ContainerStart(ctx, id, client.ContainerStartOptions{})
	return err
}

// StopContainer gracefully stops a container.
func (c *Client) StopContainer(ctx context.Context, id string) error {
	_, err := c.cli.ContainerStop(ctx, id, client.ContainerStopOptions{})
	return err
}

// KillContainer immediately kills a container.
func (c *Client) KillContainer(ctx context.Context, id string) error {
	_, err := c.cli.ContainerKill(ctx, id, client.ContainerKillOptions{Signal: "SIGKILL"})
	return err
}

// RestartContainer restarts a container.
func (c *Client) RestartContainer(ctx context.Context, id string) error {
	_, err := c.cli.ContainerRestart(ctx, id, client.ContainerRestartOptions{})
	return err
}

// RemoveContainer removes a container.
func (c *Client) RemoveContainer(ctx context.Context, id string) error {
	_, err := c.cli.ContainerRemove(ctx, id, client.ContainerRemoveOptions{Force: true})
	return err
}

// RemoveNetwork removes a network; a missing one is not an error.
func (c *Client) RemoveNetwork(ctx context.Context, name string) error {
	_, err := c.cli.NetworkRemove(ctx, name, client.NetworkRemoveOptions{})
	if err != nil && cerrdefs.IsNotFound(err) {
		return nil
	}
	return err
}

// ContainerLogs returns a reader for a container's logs.
func (c *Client) ContainerLogs(ctx context.Context, id string, opts client.ContainerLogsOptions) (io.ReadCloser, error) {
	return c.cli.ContainerLogs(ctx, id, opts)
}

// ContainerExecCreate creates an exec command in a container.
func (c *Client) ContainerExecCreate(ctx context.Context, id string, cmd []string, tty bool) (string, error) {
	env := []string{"TERM=xterm-256color"}
	resp, err := c.cli.ExecCreate(ctx, id, client.ExecCreateOptions{
		Cmd:          cmd,
		Env:          env,
		AttachStdin:  true,
		AttachStdout: true,
		AttachStderr: true,
		TTY:          tty,
	})
	if err != nil {
		return "", err
	}
	return resp.ID, nil
}

// ContainerExecAttach attaches stdin/stdout/stderr to an exec.
func (c *Client) ContainerExecAttach(ctx context.Context, execID string, tty bool) (client.HijackedResponse, error) {
	res, err := c.cli.ExecAttach(ctx, execID, client.ExecAttachOptions{TTY: tty})
	return res.HijackedResponse, err
}

// ContainerExecCreateReadOnly creates a read-only exec command (no stdin).
// Used for commands like tail where only stdout/stderr output is needed.
func (c *Client) ContainerExecCreateReadOnly(ctx context.Context, id string, cmd []string) (string, error) {
	resp, err := c.cli.ExecCreate(ctx, id, client.ExecCreateOptions{
		Cmd:          cmd,
		AttachStdin:  false,
		AttachStdout: true,
		AttachStderr: true,
		TTY:          false,
	})
	if err != nil {
		return "", err
	}
	return resp.ID, nil
}

// ContainerExecResize resizes the TTY of an exec process.
func (c *Client) ContainerExecResize(ctx context.Context, execID string, cols, rows uint) error {
	_, err := c.cli.ExecResize(ctx, execID, client.ExecResizeOptions{
		Width:  cols,
		Height: rows,
	})
	return err
}

// CopyFromContainer returns a tar archive of the given path inside the container.
func (c *Client) CopyFromContainer(ctx context.Context, id, path string) (io.ReadCloser, error) {
	res, err := c.cli.CopyFromContainer(ctx, id, client.CopyFromContainerOptions{SourcePath: path})
	return res.Content, err
}

// CopyToContainer copies a tar archive into the container at the given path.
func (c *Client) CopyToContainer(ctx context.Context, id, path string, content io.Reader) error {
	_, err := c.cli.CopyToContainer(ctx, id, client.CopyToContainerOptions{
		DestinationPath: path,
		Content:         content,
	})
	return err
}

// StatPath returns metadata about a path inside the container. It is used to
// decide whether a copy destination is an existing directory or a new name.
func (c *Client) StatPath(ctx context.Context, id, path string) (container.PathStat, error) {
	res, err := c.cli.ContainerStatPath(ctx, id, client.ContainerStatPathOptions{Path: path})
	return res.Stat, err
}

// refreshSelfNetCache re-reads the API container's networks from Docker.
func (c *Client) refreshSelfNetCache(ctx context.Context) {
	res, err := c.cli.ContainerInspect(ctx, c.selfID, client.ContainerInspectOptions{})
	if err != nil {
		c.logger.Warn("failed to refresh self network cache", "error", err)
		return
	}
	info := res.Container
	nets := make(map[string]bool, len(info.NetworkSettings.Networks))
	for name := range info.NetworkSettings.Networks {
		nets[name] = true
	}
	c.selfNetMu.Lock()
	c.selfNetCache = nets
	c.selfNetMu.Unlock()
}

// invalidateSelfNetCache is called by NetworkBridge after connect/disconnect
// to force a refresh of the cached self networks.
func (c *Client) invalidateSelfNetCache() {
	c.refreshSelfNetCache(context.Background())
}

// SelfNetworks returns the cached set of network names for this container.
func (c *Client) SelfNetworks() map[string]bool {
	c.selfNetMu.RLock()
	defer c.selfNetMu.RUnlock()
	return c.selfNetCache
}

// Close closes the Docker client and disconnects from any bridged networks.
func (c *Client) Close() error {
	if c.netBridge != nil {
		c.netBridge.Close()
	}
	return c.cli.Close()
}

// EnsureNetwork creates a bridge network if it does not exist. It reports
// whether it created it and whether the network is internal (no outbound
// access); an existing network is never modified.
func (c *Client) EnsureNetwork(ctx context.Context, name string, internal bool, labels map[string]string) (created, isInternal bool, err error) {
	res, err := c.cli.NetworkInspect(ctx, name, client.NetworkInspectOptions{})
	if err == nil {
		// NetworkInspect also resolves ID prefixes: insist on the name.
		if res.Network.Name != name || res.Network.Driver != "bridge" {
			return false, false, fmt.Errorf("network %q exists but is not a local bridge network of this name", name)
		}
		return false, res.Network.Internal, nil
	}
	if !cerrdefs.IsNotFound(err) {
		return false, false, err
	}
	if _, err := c.cli.NetworkCreate(ctx, name, client.NetworkCreateOptions{Driver: "bridge", Internal: internal, Labels: labels}); err != nil {
		return false, false, err
	}
	return true, internal, nil
}
