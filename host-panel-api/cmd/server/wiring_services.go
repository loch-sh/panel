package main

import (
	"context"
	"errors"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"host-panel-api/internal/composekit"
	"host-panel-api/internal/config"
	"host-panel-api/internal/docker"
	"host-panel-api/internal/handler"
	"host-panel-api/internal/networks"
	"host-panel-api/internal/releases"
	"host-panel-api/internal/services"
	"host-panel-api/internal/services/elasticmq"
	"host-panel-api/internal/services/mongodb"
	"host-panel-api/internal/services/mosquitto"
	"host-panel-api/internal/services/mysqlfamily"
	"host-panel-api/internal/services/nats"
	"host-panel-api/internal/services/postgres"
	"host-panel-api/internal/services/rabbitmq"
	"host-panel-api/internal/services/redisfamily"
	"host-panel-api/internal/services/s3"
)

// serviceEngines lists every engine the panel supports.
func serviceEngines() []services.Engine {
	return []services.Engine{
		mysqlfamily.NewMySQL(), mysqlfamily.NewMariaDB(), postgres.New(), mongodb.New(),
		redisfamily.NewRedis(), redisfamily.NewValkey(),
		rabbitmq.New(), nats.New(),
		s3.New(),
		elasticmq.New(), mosquitto.New(),
	}
}

// selectServiceEngines applies the Loch S3 mode. A Loch S3 admin key
// selects the shared mode, which is not available yet (Loch S3 does not
// isolate its tenants enough, see services-decisions.md §16): the s3 engine
// is then disabled rather than silently run in standalone mode. The rest of
// the module keeps working.
// It returns false when no engine is left (only s3 was enabled).
func selectServiceEngines(cfg *config.Config, logger *slog.Logger) ([]services.Engine, []string, map[string][]string, bool) {
	engines, enabled, versions := serviceEngines(), cfg.ServicesEngines, cfg.ServicesPinnedVersions
	if cfg.ServicesS3AdminAPIKey == "" {
		return engines, enabled, versions, true
	}
	logger.Error("services: SERVICES_S3_ADMIN_API_KEY is set, but the shared Loch S3 mode is not available yet: the s3 engine is disabled (unset the key for one instance per resource)")
	// Instances of the standalone mode keep running but are no longer
	// managed: list them so that they are not forgotten.
	if orphans, _ := filepath.Glob(filepath.Join(cfg.HostingStacksDir, services.InstancePrefix+"s3-*")); len(orphans) > 0 {
		names := make([]string, len(orphans))
		for i, o := range orphans {
			names[i] = filepath.Base(o)
		}
		logger.Warn("services: Loch S3 instances are no longer managed while SERVICES_S3_ADMIN_API_KEY is set (they keep running): unset the key to manage them", "instances", strings.Join(names, ","))
	}
	engines = slices.DeleteFunc(engines, func(e services.Engine) bool { return e.ID() == "s3" })
	if _, ok := versions["s3"]; ok {
		versions = maps.Clone(versions)
		delete(versions, "s3")
	}
	if len(enabled) > 0 {
		enabled = slices.DeleteFunc(slices.Clone(enabled), func(id string) bool { return id == "s3" })
		if len(enabled) == 0 {
			return nil, nil, nil, false
		}
	}
	return engines, enabled, versions, true
}

// newServicesHandler builds the services module. Unlike hosting, a failed
// check does not stop the server: the module stays disabled and the reason
// is shown to admins (GET /services/config).
func newServicesHandler(ctx context.Context, cfg *config.Config, dockerClient *docker.Client, cache *docker.ContainerCache, nets *networks.Manager, logger, auditLogger *slog.Logger) *handler.ServicesHandler {
	h := &handler.ServicesHandler{Network: cfg.ServicesNetwork, Logger: logger}
	if !cfg.ServicesEnabled {
		return h
	}
	disable := func(reason string, err error) *handler.ServicesHandler {
		// The details (host paths, daemon errors) only go to the logs.
		logger.Error("services module disabled: "+reason, "error", err)
		h.ConfigError = handler.ConfigErrorCode
		return h
	}
	engines, enabled, versions, ok := selectServiceEngines(cfg, logger)
	if !ok {
		return disable("invalid configuration", errors.New("SERVICES_ENGINES only enables s3, which SERVICES_S3_ADMIN_API_KEY disables"))
	}
	// Configuration errors stop the API (like config.Load): only the
	// installation checks below disable the module without stopping it.
	fatal := func(variable string, err error) {
		logger.Error("invalid configuration", "variable", variable, "problem", err)
		os.Exit(1)
	}
	reg, err := services.NewRegistry(engines, enabled, versions, nil)
	if err != nil {
		fatal("SERVICES_ENGINES / SERVICES_<ENGINE>_PINNED_VERSIONS", err)
	}
	reserved := []int{22, 80, 443, cfg.Port}
	if p, err := strconv.Atoi(cfg.SFTPPort); err == nil {
		reserved = append(reserved, p)
	}
	cidrs, err := services.ParseAllowedCIDRs(cfg.ServicesExposeCIDRs, cfg.ServicesAllowPublicBind)
	if err != nil {
		fatal("SERVICES_EXPOSE_ALLOWED_CIDRS", err) // already linted by config.Load
	}
	var certificate *services.Certificate
	if cfg.ServicesTLSCertFile != "" {
		certPEM, cerr := os.ReadFile(cfg.ServicesTLSCertFile)
		keyPEM, kerr := os.ReadFile(cfg.ServicesTLSKeyFile)
		c := services.Certificate{CertPEM: certPEM, KeyPEM: keyPEM}
		if err := errors.Join(cerr, kerr); err != nil {
			fatal("SERVICES_TLS_CERT_FILE", err)
		}
		if err := services.ValidateCertificate(c, time.Now()); err != nil {
			fatal("SERVICES_TLS_CERT_FILE", err)
		}
		certificate = &c
	}
	svcCfg := services.Config{
		StacksDir:               cfg.HostingStacksDir,
		Render:                  services.RenderConfig{DataDir: cfg.ServicesDataDir, Networks: nets},
		MaxResourcesPerInstance: cfg.ServicesMaxResources,
		MaxDedicatedInstances:   cfg.ServicesMaxDedicated,
		ArchiveOnRetire:         cfg.ServicesArchiveOnRetire,
		Exposure: services.ExposurePolicy{
			AllowedCIDRs: cidrs, AllowTLSOptional: cfg.ServicesAllowTLSOptional, ReservedPorts: reserved,
		},
		TLS:         services.TLSMode(cfg.ServicesTLS),
		PublicHost:  cfg.ServicesPublicHost,
		Certificate: certificate,
		Releases:    releases.New(),
	}
	// The last discovered catalog (a cache: a broken file is rewritten by
	// the next discovery).
	if err := reg.SetStore(cfg.DataDir); err != nil {
		logger.Warn("services: the discovered catalog could not be read, using the built-in one until the next discovery", "error", err)
	}
	// compose.override.yaml holds the published port and TLS (see services).
	runner := composekit.ExecRunner{Files: []string{composekit.DefaultComposeFile, services.OverrideFile}}
	// The daemon may still be starting with the host: retry a few times.
	var check services.PreflightResult
	for attempt := 1; ; attempt++ {
		pctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		check, err = services.Preflight(pctx, svcCfg, runner, dockerClient)
		if err == nil {
			err = nets.EnsureShared(pctx, cfg.ServicesNetwork)
		}
		cancel()
		if err == nil {
			break
		}
		if attempt == 3 {
			return disable("installation check failed", err)
		}
		logger.Warn("services: installation check failed, retrying", "attempt", attempt, "error", err)
		time.Sleep(5 * time.Second)
	}
	mgr := services.NewManager(svcCfg, reg, runner, dockerClient, logger, auditLogger, cache.Invalidate)
	h.Manager = mgr
	h.Registry = reg
	h.Enabled = true
	nets.AddSource(networks.KindInstance, services.NetworkSource{M: mgr})
	if cfg.ServicesAllowPublicBind {
		logger.Warn("services: ports may be published on non-loopback addresses: Docker bypasses the host firewall (ufw, firewalld)",
			"allowed", cfg.ServicesExposeCIDRs)
	}
	if cfg.ServicesTLS == string(services.TLSOff) {
		logger.Warn("services: TLS disabled (SERVICES_TLS=off): passwords and data travel in clear text on the published ports")
	}
	// Instances created before images were recorded keep the image they
	// were created with (before any discovery moves the catalog).
	mgr.RecordImages(ctx)
	// Instances created before images were pinned by digest are pinned to
	// the image their container runs (no restart).
	mgr.PinImages(ctx)
	// Apply the published ports in the background: an instance being
	// recreated must not delay the API.
	go mgr.ApplyExposures(ctx)
	// Discover the versions and updates periodically (and on demand).
	go mgr.RunUpdateChecks(ctx, time.Duration(cfg.ServicesUpdateCheckHours)*time.Hour)
	var engineIDs []string
	for _, e := range reg.Engines() {
		engineIDs = append(engineIDs, e.ID())
	}
	logger.Info("services module enabled",
		"stacks_dir", cfg.HostingStacksDir,
		"data_dir", cfg.ServicesDataDir,
		"network", cfg.ServicesNetwork,
		"engines", strings.Join(engineIDs, ","),
		"compose_version", check.ComposeVersion,
	)
	return h
}
