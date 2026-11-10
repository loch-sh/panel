package main

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"host-panel-api/internal/config"
	"host-panel-api/internal/docker"
	"host-panel-api/internal/networks"
)

// newNetworksManager loads the registry of the private networks and brings
// back the ones missing from Docker (the members' compose files reference
// them). The registry is required: the API does not start without it.
func newNetworksManager(cfg *config.Config, dockerClient *docker.Client, logger, auditLogger *slog.Logger) *networks.Manager {
	m, err := networks.NewManager(networks.Config{
		File: filepath.Join(cfg.DataDir, "networks.json"), Max: cfg.NetworksMax, Reserved: cfg.NetworksReservedRanges,
	}, dockerClient, logger, auditLogger)
	if err != nil {
		logger.Error("networks: cannot load the registry", "error", err)
		os.Exit(1)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	m.Reconcile(ctx)
	return m
}
