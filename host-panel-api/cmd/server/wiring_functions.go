package main

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"host-panel-api/internal/composekit"
	"host-panel-api/internal/config"
	"host-panel-api/internal/docker"
	"host-panel-api/internal/functions"
	"host-panel-api/internal/handler"
	"host-panel-api/internal/networks"
	"host-panel-api/internal/releases"
)

// newFunctionsHandler builds the functions module. Like services, a failed
// installation check disables the module only (GET /functions/config
// reports FUNCTIONS_MISCONFIGURED, the details go to the logs); the
// configuration itself was linted by config.Load.
// onDelete is called after a function was deleted (cron jobs).
func newFunctionsHandler(ctx context.Context, cfg *config.Config, dockerClient *docker.Client, cache *docker.ContainerCache, nets *networks.Manager, onDelete func(name, actor string), logger, auditLogger *slog.Logger) *handler.FunctionsHandler {
	fcfg := cfg.Functions()
	h := &handler.FunctionsHandler{MaxCodeBytes: fcfg.MaxCodeBytes, Logger: logger}
	if !cfg.FunctionsEnabled {
		return h
	}
	disable := func(reason string, err error) *handler.FunctionsHandler {
		logger.Error("functions module disabled: "+reason, "error", err)
		h.ConfigError = handler.FunctionsConfigErrorCode
		return h
	}
	runner := composekit.ExecRunner{}
	var err error
	var composeVersion, arch string
	var ncpu int
	var memTotal int64
	for attempt := 1; ; attempt++ {
		pctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		composeVersion, err = functions.Preflight(pctx, fcfg, runner, dockerClient)
		if err == nil {
			arch, err = dockerClient.HostArch(pctx)
		}
		if err == nil {
			ncpu, memTotal, err = dockerClient.HostResources(pctx)
		}
		cancel()
		if err == nil {
			break
		}
		if attempt == 3 {
			return disable("installation check failed", err)
		}
		logger.Warn("functions: installation check failed, retrying", "attempt", attempt, "error", err)
		time.Sleep(5 * time.Second)
	}
	if !functions.ValidArch(arch) {
		return disable("unsupported Docker host architecture", errors.New(arch))
	}
	mcfg := functions.ManagerConfig{
		Config: fcfg, Arch: arch,
		MaxTotalMemoryMB: cfg.FunctionsMaxTotalMemoryMB, MaxTotalCPUs: cfg.FunctionsMaxTotalCPUs,
	}
	if mcfg.MaxTotalMemoryMB == 0 {
		mcfg.MaxTotalMemoryMB = int(memTotal >> 20)
	}
	if mcfg.MaxTotalCPUs == 0 {
		mcfg.MaxTotalCPUs = float64(4 * ncpu)
	}
	// The runtime releases accepted so far (GitHub sha256, never changed).
	accepted, err := functions.LoadRuntimeRecord(fcfg.Paths())
	if err != nil {
		return disable("invalid runtime record", err)
	}
	store := functions.NewRuntimeStore(fcfg.Paths(), accepted)
	var source functions.RuntimeSource
	switch {
	case cfg.FunctionsRuntimeSourceDir != "":
		source = functions.RuntimeSourceDir{Dir: cfg.FunctionsRuntimeSourceDir}
	case cfg.FunctionsRuntimeDownload:
		source = functions.NewRuntimeDownloader()
	}
	runtimes := functions.NewRuntimeProvider(store, arch, source, logger, auditLogger)
	mgr := functions.NewManager(mcfg, functions.Deps{
		Runner: runner, Docker: dockerClient, Networks: nets, Runtimes: runtimes,
		Digests: releases.New(), Invoker: functions.ExecInvoker{Docker: dockerClient},
		Logger: logger, Audit: auditLogger, OnChange: cache.Invalidate, OnDelete: onDelete,
	})
	// Before any upload is accepted.
	mgr.Startup(ctx)
	go mgr.RunAudit(ctx)
	nets.AddSource(networks.KindFunction, functions.NetworkSource{M: mgr})
	h.Manager = mgr
	h.FlushAudit = mgr.CloseAudit
	h.Enabled = true
	// In the background: the module answers FUNCTION_RUNTIME_UNAVAILABLE
	// to the operations needing the binary meanwhile.
	go runtimes.Run(ctx)
	logger.Info("functions module enabled", "data_dir", fcfg.DataDir, "stacks_dir", fcfg.StacksDir, "arch", arch,
		"max_total_memory_mb", mcfg.MaxTotalMemoryMB, "max_total_cpus", mcfg.MaxTotalCPUs, "compose_version", composeVersion)
	return h
}
