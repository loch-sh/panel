package docker

import (
	"context"
	"fmt"
	"strings"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/moby/moby/client"
)

// PullImage pulls an image (anonymous access) and waits for the end of the
// pull. platform is "" (the daemon's) or "os/arch" (e.g. "linux/amd64").
func (c *Client) PullImage(ctx context.Context, ref, platform string) error {
	opts := client.ImagePullOptions{}
	if platform != "" {
		osName, arch, ok := strings.Cut(platform, "/")
		if !ok || osName == "" || arch == "" {
			return fmt.Errorf("invalid platform %q", platform)
		}
		opts.Platforms = []ocispec.Platform{{OS: osName, Architecture: arch}}
	}
	resp, err := c.cli.ImagePull(ctx, ref, opts)
	if err != nil {
		return err
	}
	defer resp.Close()
	// Wait reports the error of the stream (e.g. manifest unknown).
	return resp.Wait(ctx)
}
