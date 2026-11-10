package ws

import (
	"context"
	"log/slog"
	"regexp"
	"strings"
	"sync"
	"time"

	"host-panel-api/internal/docker"
	"host-panel-api/internal/model"
)

var validContainerID = regexp.MustCompile(`^[a-f0-9]{12,64}$`)

// statsTarget identifies a container to monitor.
type statsTarget struct {
	id   string
	name string
}

// HandleStatsStart starts streaming container stats to the WebSocket.
// It collects stats for the given container IDs every 2 seconds.
// Returns a cancel function to stop the streaming.
func HandleStatsStart(
	ctx context.Context,
	conn *Conn,
	client *docker.Client,
	user *model.User,
	containerIDs []string,
	logger *slog.Logger,
) (context.CancelFunc, error) {
	// Cap input early to bound the number of Docker inspect calls
	if len(containerIDs) > 20 {
		containerIDs = containerIDs[:20]
	}

	// Resolve names and filter by permissions
	var targets []statsTarget

	for _, id := range containerIDs {
		if !validContainerID.MatchString(id) {
			continue
		}
		info, err := client.InspectContainer(ctx, id)
		if err != nil {
			continue
		}
		name := strings.TrimPrefix(info.Name, "/")

		// Shared check (never a function container for a restricted user).
		if !docker.UserCanAccess(user, info) {
			continue
		}

		targets = append(targets, statsTarget{id: id, name: name})
	}

	if len(targets) == 0 {
		conn.WriteEvent("statsError", "No accessible containers")
		return nil, nil
	}

	// Cap to 20 containers max
	if len(targets) > 20 {
		targets = targets[:20]
	}

	statsCtx, cancel := context.WithCancel(ctx)

	go func() {
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()

		// Collect and send immediately, then on each tick
		for {
			stats := collectStats(statsCtx, client, targets, logger)
			if statsCtx.Err() != nil {
				return
			}
			conn.WriteEvent("statsData", stats)

			select {
			case <-statsCtx.Done():
				return
			case <-ticker.C:
			}
		}
	}()

	return cancel, nil
}

// collectStats fetches stats for all targets in parallel.
func collectStats(
	ctx context.Context,
	client *docker.Client,
	targets []statsTarget,
	logger *slog.Logger,
) []model.ContainerStats {
	results := make([]model.ContainerStats, len(targets))
	var wg sync.WaitGroup

	for i, t := range targets {
		wg.Add(1)
		go func(idx int, id, name string) {
			defer wg.Done()
			s, err := client.ContainerStatsOneShotByName(ctx, id, name)
			if err != nil {
				if ctx.Err() == nil {
					logger.Warn("stats fetch failed", "container", name, "error", err)
				}
				results[idx] = model.ContainerStats{ID: id, Name: name}
				return
			}
			results[idx] = *s
		}(i, t.id, t.name)
	}

	wg.Wait()
	return results
}
