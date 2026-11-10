package main

import (
	"context"
	"log/slog"
	"os"
	"time"

	"host-panel-api/internal/config"
	"host-panel-api/internal/docker"
	"host-panel-api/internal/handler"
	"host-panel-api/internal/hosting"
	"host-panel-api/internal/networks"
)

// newHostingHandler builds the hosting plugin. When enabled, the installation
// is verified first (directories, docker compose, same-path mounts) and the
// server refuses to start on failure rather than generating wrong paths.
func newHostingHandler(cfg *config.Config, dockerClient *docker.Client, cache *docker.ContainerCache, nets *networks.Manager, logger, auditLogger *slog.Logger) *handler.HostingHandler {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if cfg.HostingEnabled {
		// Bound per-stack limits by what the host actually has.
		cpus, mem, err := dockerClient.HostResources(ctx)
		if err != nil {
			logger.Error("hosting plugin: cannot read the host capacity", "error", err)
			os.Exit(1)
		}
		capped, err := cfg.ResolveHostingMax(cpus, mem)
		if err != nil {
			logger.Error("hosting plugin: invalid resource defaults", "error", err)
			os.Exit(1)
		}
		for _, name := range capped {
			logger.Warn("hosting plugin: maximum above the host capacity, capped", "variable", name)
		}
	}

	limits := hosting.Limits{
		MaxMemoryMB:     cfg.HostingMaxMemoryMB,
		MaxCPUs:         cfg.HostingMaxCPUs,
		MaxDomains:      20,
		MaxEnvVars:      100,
		MaxSnippetBytes: 64 << 10,
	}
	h := &handler.HostingHandler{
		Enabled:  cfg.HostingEnabled,
		SitesDir: cfg.HostingSitesDir,
		Limits:   limits,
		Defaults: handler.HostingDefaults{
			PHPVersion:   hosting.DefaultPHPVersion,
			DocumentRoot: "web",
			PHP: hosting.PHPSettings{
				MemoryLimit:       "256M",
				UploadMaxFilesize: "64M",
				PostMaxSize:       "64M",
				MaxExecutionTime:  60,
				Timezone:          "UTC",
				DisabledModules:   hosting.DefaultDisabledModules,
			},
			Resources: hosting.Resources{MemoryMB: cfg.HostingDefaultMemoryMB, CPUs: cfg.HostingDefaultCPUs},
		},
		Logger: logger,
	}
	if !cfg.HostingEnabled {
		return h
	}

	hostingCfg := hosting.Config{
		StacksDir: cfg.HostingStacksDir,
		Render: hosting.RenderConfig{
			SitesDir:        cfg.HostingSitesDir,
			TraefikNetwork:  cfg.HostingTraefikNetwork,
			HTTPEntrypoint:  cfg.HostingHTTPEntrypoint,
			HTTPSEntrypoint: cfg.HostingHTTPSEntrypoint,
			CertResolver:    cfg.HostingCertResolver,
			Networks:        nets,
		},
		RunUID: cfg.HostingRunUID,
		RunGID: cfg.HostingRunGID,
		Limits: limits,
	}
	runner := hosting.ExecRunner{}
	version, err := hosting.Preflight(ctx, hostingCfg, runner, dockerClient)
	if err != nil {
		logger.Error("hosting plugin: installation check failed", "error", err)
		os.Exit(1)
	}
	mgr := hosting.NewManager(hostingCfg, runner, dockerClient, &hosting.DockerModuleInspector{}, logger, auditLogger, cache.Invalidate)
	h.Manager = mgr
	nets.AddSource(networks.KindSite, hosting.NetworkSource{M: mgr})
	logger.Info("hosting plugin enabled",
		"stacks_dir", cfg.HostingStacksDir,
		"sites_dir", cfg.HostingSitesDir,
		"traefik_network", cfg.HostingTraefikNetwork,
		"compose_version", version,
		"max_memory_mb", cfg.HostingMaxMemoryMB,
		"max_cpus", cfg.HostingMaxCPUs,
	)
	return h
}
