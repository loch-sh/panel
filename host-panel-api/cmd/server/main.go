package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"host-panel-api/internal/audit"
	"host-panel-api/internal/auth"
	"host-panel-api/internal/config"
	"host-panel-api/internal/cron"
	"host-panel-api/internal/docker"
	"host-panel-api/internal/env"
	"host-panel-api/internal/functions"
	"host-panel-api/internal/handler"
	"host-panel-api/internal/middleware"
	"host-panel-api/internal/networks"
	"host-panel-api/internal/sftpgo"
	"host-panel-api/internal/store"
	"host-panel-api/internal/ws"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		// Every problem at once: the API never starts with a configuration
		// it would partly ignore.
		if problems, ok := env.AsProblems(err); ok {
			for _, p := range problems {
				logger.Error("invalid configuration", "variable", p.Var, "problem", p.Message)
			}
			logger.Error("refusing to start", "problems", len(problems))
		} else {
			logger.Error("failed to load configuration", "error", err)
		}
		os.Exit(1)
	}

	middleware.SetTrustedProxies(cfg.TrustedProxies)
	if len(cfg.TrustedProxies) == 0 {
		logger.Warn("TRUSTED_PROXIES is empty: only the loopback may set X-Forwarded-For; behind a reverse proxy in Docker (Traefik), every client shares the proxy address for the rate limit and the audit trail: set the subnet of the proxy network")
	}

	if cfg.Env == "development" {
		logger = slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
			Level: slog.LevelDebug,
		}))
	}

	// Initialize audit logger (file-based)
	auditLogger, auditFile, err := audit.New(cfg.LogDir)
	if err != nil {
		logger.Error("failed to initialize audit logger", "error", err)
		os.Exit(1)
	}
	defer auditFile.Close()

	logger.Info("starting server",
		"env", cfg.Env,
		"port", cfg.Port,
		"oidc_provider", cfg.OIDCProvider,
	)

	// Initialize the user store
	usersPath := filepath.Join(cfg.DataDir, "users.json")
	userStore, err := store.NewJSONFileStore(usersPath, logger)
	if err != nil {
		logger.Error("failed to initialize store", "error", err)
		os.Exit(1)
	}

	// Initialize the API key store
	apiKeysPath := filepath.Join(cfg.DataDir, "apikeys.json")
	apiKeyStore, err := store.NewAPIKeyFileStore(apiKeysPath, logger)
	if err != nil {
		logger.Error("failed to initialize api key store", "error", err)
		os.Exit(1)
	}

	// Initialize the Docker client
	dockerClient, err := docker.NewClient(logger)
	if err != nil {
		logger.Error("failed to connect to Docker", "error", err)
		os.Exit(1)
	}
	defer dockerClient.Close()

	containerCache := docker.NewContainerCache(dockerClient, 3*time.Second)

	// Initialize the cron module
	cronPath := filepath.Join(cfg.DataDir, "cron.json")
	cronStore, err := cron.NewStore(cronPath, logger)
	if err != nil {
		logger.Error("failed to initialize cron store", "error", err)
		os.Exit(1)
	}
	cronExecutor := cron.NewExecutor(dockerClient, cronStore, logger, auditLogger, time.Duration(cfg.CronHTTPTimeout)*time.Second)
	cronScheduler := cron.NewScheduler(cronStore, cronExecutor, logger)
	// Started once the functions module is wired (function_invoke jobs are
	// scheduled only with it, and checked against its functions). Nothing
	// needs it running earlier: the handlers only register jobs, and the
	// HTTP server starts after it.

	// Initialize the OIDC provider
	ctx := context.Background()
	var provider auth.OIDCProvider

	switch cfg.OIDCProvider {
	case "microsoft":
		provider, err = auth.NewMicrosoftProvider(ctx, cfg)
		if err != nil {
			logger.Error("failed to initialize Microsoft OIDC", "error", err)
			os.Exit(1)
		}
	case "keycloak":
		provider, err = auth.NewKeycloakProvider(ctx, cfg)
		if err != nil {
			logger.Error("failed to initialize Keycloak OIDC", "error", err)
			os.Exit(1)
		}
	case "generic":
		provider, err = auth.NewGenericProvider(ctx, cfg)
		if err != nil {
			logger.Error("failed to initialize generic OIDC", "error", err)
			os.Exit(1)
		}
	default:
		logger.Error("unknown OIDC provider", "provider", cfg.OIDCProvider)
		os.Exit(1)
	}

	logger.Info("OIDC provider initialized", "provider", provider.Name())

	// Initialize JWT verification
	verifier := auth.NewJWKSVerifier(provider.JWKSURL(), provider.Issuer(), provider.Audience())
	cookieMgr := auth.NewCookieManager(cfg.CookieSecret)

	// Initialize the WebSocket hub
	hub := ws.NewHub(logger)

	// Create handlers
	healthHandler := &handler.HealthHandler{Docker: dockerClient}
	authHandler := &handler.AuthHandler{
		Provider: provider,
		Cookies:  cookieMgr,
		Cfg:      cfg,
		Logger:   logger,
		Audit:    auditLogger,
	}
	dockerHandler := &handler.DockerHandler{
		Cache:  containerCache,
		Client: dockerClient,
		Logger: logger,
		Audit:  auditLogger,
	}
	logsHandler := &handler.LogsHandler{
		Client: dockerClient,
		Logger: logger,
		Audit:  auditLogger,
	}
	usersHandler := &handler.UsersHandler{
		Store:       userStore,
		APIKeyStore: apiKeyStore,
		Logger:      logger,
		Audit:       auditLogger,
	}
	cronHandler := &handler.CronHandler{
		Store:       cronStore,
		Scheduler:   cronScheduler,
		Docker:      dockerClient,
		Logger:      logger,
		Audit:       auditLogger,
		MaxJobs:     cfg.CronMaxJobs,
		HTTPTimeout: cfg.CronHTTPTimeout,
	}
	domainHandler := &handler.DomainHandler{
		Cache:  containerCache,
		Logger: logger,
	}
	apiKeyHandler := &handler.APIKeyHandler{
		Store:  apiKeyStore,
		Logger: logger,
		Audit:  auditLogger,
	}
	// SFTP plugin (optional). The client is built only when enabled.
	sftpHandler := &handler.SFTPHandler{
		Enabled:          cfg.SFTPEnabled,
		Directory:        strings.TrimRight(cfg.SFTPDirectory, "/"),
		Domain:           cfg.SFTPDomain,
		Port:             cfg.SFTPPort,
		WebUserInterface: cfg.SFTPWebUserInterface,
		InitDirs:         cfg.SFTPInitDirs,
		Logger:           logger,
		Audit:            auditLogger,
	}
	if cfg.SFTPEnabled {
		sftpHandler.Client = sftpgo.NewClient(cfg.SFTPAPIURL, cfg.SFTPAPIKey, logger)
		logger.Info("SFTP plugin enabled", "directory", cfg.SFTPDirectory, "api_url", cfg.SFTPAPIURL)
	}
	networksManager := newNetworksManager(cfg, dockerClient, logger, auditLogger)
	hostingHandler := newHostingHandler(cfg, dockerClient, containerCache, networksManager, logger, auditLogger)
	// Background work of the services module stops with the server.
	servicesCtx, stopServices := context.WithCancel(context.Background())
	defer stopServices()
	servicesHandler := newServicesHandler(servicesCtx, cfg, dockerClient, containerCache, networksManager, logger, auditLogger)
	if _, shared := networksManager.Shared(); shared && servicesHandler.Enabled {
		hostingHandler.DefaultNetworks = []string{networks.SharedName}
	}
	functionsHandler := newFunctionsHandler(servicesCtx, cfg, dockerClient, containerCache, networksManager, cronHandler.FunctionDeleted, logger, auditLogger)
	// Cron function_invoke jobs go through the module (docker exec into the
	// verified container); they are refused while it is disabled.
	if fm, ok := functionsHandler.Manager.(*functions.Manager); ok && functionsHandler.Enabled {
		cronHandler.Functions = fm
		cronExecutor.SetFunctions(fm)
	}
	cronScheduler.Start()
	networksHandler := &handler.NetworksHandler{Manager: networksManager, Logger: logger}
	wsHandler := &handler.WSHandler{
		Hub:      hub,
		Docker:   dockerClient,
		Cookies:  cookieMgr,
		Verifier: verifier,
		Store:    userStore,
		Logger:   logger,
		Audit:    auditLogger,
		FrontURL: cfg.FrontURL,
	}

	// Helpers for middleware chains.
	// APIKeyOrJWT checks X-API-Key first; if present, validates and injects user
	// directly. If absent, falls through to the JWT + User middleware chain.
	authenticated := func(h http.Handler) http.Handler {
		jwtFlow := middleware.Chain(h,
			middleware.JWT(verifier, cookieMgr),
			middleware.User(userStore, logger, auditLogger),
		)
		return middleware.APIKeyOrJWT(apiKeyStore, userStore, logger, h, jwtFlow)
	}

	adminOnly := func(h http.Handler) http.Handler {
		adminH := middleware.Admin()(h)
		jwtFlow := middleware.Chain(h,
			middleware.JWT(verifier, cookieMgr),
			middleware.User(userStore, logger, auditLogger),
			middleware.Admin(),
		)
		return middleware.APIKeyOrJWT(apiKeyStore, userStore, logger, adminH, jwtFlow)
	}

	// Configure routes
	mux := http.NewServeMux()

	// Health (public)
	mux.Handle("GET /_health", healthHandler)

	// OIDC (public)
	mux.HandleFunc("GET /openid-connect/login", authHandler.Login)
	mux.HandleFunc("GET /openid-connect/redirect", authHandler.Redirect)
	mux.HandleFunc("GET /openid-connect/logout", authHandler.Logout)

	// Docker (authenticated)
	mux.Handle("GET /docker", authenticated(http.HandlerFunc(dockerHandler.List)))
	mux.Handle("GET /docker/{id}", authenticated(http.HandlerFunc(dockerHandler.Inspect)))
	mux.Handle("POST /docker/{id}/{action}", authenticated(http.HandlerFunc(dockerHandler.Action)))

	// Compose stack actions (authenticated, permissions checked per container)
	stackHandler := &handler.StackHandler{
		Cache:  containerCache,
		Client: dockerClient,
		Logger: logger,
		Audit:  auditLogger,
	}
	mux.Handle("POST /stacks/{name}/{action}", authenticated(http.HandlerFunc(stackHandler.Action)))

	// Container file copy (authenticated)
	cpHandler := &handler.CpHandler{
		Client: dockerClient,
		Logger: logger,
		Audit:  auditLogger,
	}
	mux.Handle("GET /docker/{id}/cp", authenticated(http.HandlerFunc(cpHandler.Download)))
	mux.Handle("PUT /docker/{id}/cp", authenticated(http.HandlerFunc(cpHandler.Upload)))

	// Traefik domains (admin only)
	mux.Handle("GET /domains", adminOnly(http.HandlerFunc(domainHandler.List)))

	// Logs (authenticated)
	mux.Handle("GET /logs/{id}", authenticated(http.HandlerFunc(logsHandler.Download)))
	mux.Handle("GET /logs/{id}/json", authenticated(http.HandlerFunc(logsHandler.JSON)))
	mux.Handle("GET /logs/{id}/file", authenticated(http.HandlerFunc(logsHandler.FileTail)))

	// API Keys (authenticated)
	mux.Handle("GET /users/me/api-keys", authenticated(http.HandlerFunc(apiKeyHandler.List)))
	mux.Handle("POST /users/me/api-keys", authenticated(http.HandlerFunc(apiKeyHandler.Create)))
	mux.Handle("DELETE /users/me/api-keys/{id}", authenticated(http.HandlerFunc(apiKeyHandler.Delete)))

	// Users
	mux.Handle("GET /users/me", authenticated(http.HandlerFunc(usersHandler.Me)))
	mux.Handle("GET /users", adminOnly(http.HandlerFunc(usersHandler.List)))
	mux.Handle("PATCH /users/{email}/switch", adminOnly(http.HandlerFunc(usersHandler.Switch)))
	mux.Handle("PATCH /users/{email}/restrict", adminOnly(http.HandlerFunc(usersHandler.Restrict)))
	mux.Handle("PATCH /users/{email}", adminOnly(http.HandlerFunc(usersHandler.UpdateACL)))
	mux.Handle("DELETE /users/{email}", adminOnly(http.HandlerFunc(usersHandler.Delete)))

	// Cron (authenticated, permissions checked in the handler)
	mux.Handle("GET /cron", authenticated(http.HandlerFunc(cronHandler.List)))
	mux.Handle("POST /cron", authenticated(http.HandlerFunc(cronHandler.Create)))
	mux.Handle("GET /cron/{id}", authenticated(http.HandlerFunc(cronHandler.Get)))
	mux.Handle("PUT /cron/{id}", authenticated(http.HandlerFunc(cronHandler.Update)))
	mux.Handle("PATCH /cron/{id}", authenticated(http.HandlerFunc(cronHandler.Patch)))
	mux.Handle("DELETE /cron/{id}", authenticated(http.HandlerFunc(cronHandler.Delete)))
	mux.Handle("POST /cron/{id}/run", authenticated(http.HandlerFunc(cronHandler.RunNow)))

	// SFTP plugin (admin only). /sftp/config is always reachable so the
	// frontend can detect whether the plugin is enabled.
	mux.Handle("GET /sftp/config", adminOnly(http.HandlerFunc(sftpHandler.Config)))
	mux.Handle("GET /sftp/sites", adminOnly(http.HandlerFunc(sftpHandler.ListSites)))
	mux.Handle("GET /sftp/users", adminOnly(http.HandlerFunc(sftpHandler.ListUsers)))
	mux.Handle("POST /sftp/users", adminOnly(http.HandlerFunc(sftpHandler.CreateUser)))
	mux.Handle("PUT /sftp/users/{username}", adminOnly(http.HandlerFunc(sftpHandler.UpdateUser)))
	mux.Handle("DELETE /sftp/users/{username}", adminOnly(http.HandlerFunc(sftpHandler.DeleteUser)))
	mux.Handle("POST /sftp/users/{username}/password", adminOnly(http.HandlerFunc(sftpHandler.ResetPassword)))
	mux.Handle("POST /sftp/users/{username}/status", adminOnly(http.HandlerFunc(sftpHandler.SetEnabled)))

	// Hosting plugin (admin only). /hosting/config is always reachable so the
	// frontend can detect whether the plugin is enabled.
	mux.Handle("GET /hosting/config", adminOnly(http.HandlerFunc(hostingHandler.Config)))
	mux.Handle("GET /hosting/sites", adminOnly(http.HandlerFunc(hostingHandler.Sites)))
	mux.Handle("GET /hosting/php-modules", adminOnly(http.HandlerFunc(hostingHandler.PHPModules)))
	mux.Handle("GET /hosting/routing-template", adminOnly(http.HandlerFunc(hostingHandler.RoutingTemplate)))
	mux.Handle("GET /hosting/stacks", adminOnly(http.HandlerFunc(hostingHandler.List)))
	mux.Handle("POST /hosting/stacks", adminOnly(http.HandlerFunc(hostingHandler.Create)))
	mux.Handle("GET /hosting/stacks/check-name", adminOnly(http.HandlerFunc(hostingHandler.CheckName)))
	mux.Handle("GET /hosting/stacks/{name}", adminOnly(http.HandlerFunc(hostingHandler.Get)))
	mux.Handle("PUT /hosting/stacks/{name}", adminOnly(http.HandlerFunc(hostingHandler.Update)))
	mux.Handle("DELETE /hosting/stacks/{name}", adminOnly(http.HandlerFunc(hostingHandler.Delete)))
	mux.Handle("POST /hosting/stacks/{name}/deploy", adminOnly(http.HandlerFunc(hostingHandler.Deploy)))

	// Services module (admin only). /services/config is always reachable so
	// the frontend can detect whether the module is enabled.
	mux.Handle("GET /services/config", adminOnly(http.HandlerFunc(servicesHandler.Config)))
	mux.Handle("GET /services/instances", adminOnly(http.HandlerFunc(servicesHandler.ListInstances)))
	mux.Handle("DELETE /services/instances/{instance}", adminOnly(http.HandlerFunc(servicesHandler.RetireInstance)))
	mux.Handle("PATCH /services/instances/{instance}", adminOnly(http.HandlerFunc(servicesHandler.UpdateInstance)))
	mux.Handle("POST /services/instances/{instance}/update", adminOnly(http.HandlerFunc(servicesHandler.UpdateInstanceImage)))
	mux.Handle("POST /services/updates/check", adminOnly(http.HandlerFunc(servicesHandler.CheckUpdates)))
	mux.Handle("GET /services/instances/{instance}/resources", adminOnly(http.HandlerFunc(servicesHandler.ListResources)))
	mux.Handle("POST /services/resources", adminOnly(http.HandlerFunc(servicesHandler.CreateResource)))
	mux.Handle("POST /services/instances/{instance}/resources/{name}/password", adminOnly(http.HandlerFunc(servicesHandler.RotatePassword)))
	mux.Handle("DELETE /services/instances/{instance}/resources/{name}", adminOnly(http.HandlerFunc(servicesHandler.DeleteResource)))
	mux.Handle("PUT /services/instances/{instance}/networks", adminOnly(http.HandlerFunc(servicesHandler.UpdateInstanceNetworks)))
	mux.Handle("PUT /services/instances/{instance}/exposure", adminOnly(http.HandlerFunc(servicesHandler.SetExposure)))
	mux.Handle("DELETE /services/instances/{instance}/exposure", adminOnly(http.HandlerFunc(servicesHandler.DeleteExposure)))

	// Functions module (admin only). /functions/config is always reachable
	// so the frontend can decide whether to show the module.
	mux.Handle("GET /functions/config", adminOnly(http.HandlerFunc(functionsHandler.Config)))
	mux.Handle("GET /functions", adminOnly(http.HandlerFunc(functionsHandler.List)))
	mux.Handle("POST /functions", adminOnly(http.HandlerFunc(functionsHandler.Create)))
	mux.Handle("GET /functions/{name}", adminOnly(http.HandlerFunc(functionsHandler.Get)))
	mux.Handle("PATCH /functions/{name}", adminOnly(http.HandlerFunc(functionsHandler.Update)))
	mux.Handle("DELETE /functions/{name}", adminOnly(http.HandlerFunc(functionsHandler.Delete)))
	mux.Handle("POST /functions/{name}/code", adminOnly(http.HandlerFunc(functionsHandler.Deploy)))
	mux.Handle("GET /functions/{name}/deployments", adminOnly(http.HandlerFunc(functionsHandler.Deployments)))
	mux.Handle("POST /functions/{name}/deployments/{id}/activate", adminOnly(http.HandlerFunc(functionsHandler.Activate)))
	mux.Handle("PUT /functions/{name}/secrets/{key}", adminOnly(http.HandlerFunc(functionsHandler.SetSecret)))
	mux.Handle("DELETE /functions/{name}/secrets/{key}", adminOnly(http.HandlerFunc(functionsHandler.DeleteSecret)))
	mux.Handle("POST /functions/{name}/credentials/rotate", adminOnly(http.HandlerFunc(functionsHandler.RotateCredentials)))
	mux.Handle("POST /functions/{name}/invoke", adminOnly(http.HandlerFunc(functionsHandler.Invoke)))
	mux.Handle("POST /functions/{name}/{action}", adminOnly(http.HandlerFunc(functionsHandler.Action)))
	mux.Handle("PUT /functions/{name}/networks", adminOnly(http.HandlerFunc(functionsHandler.SetNetworks)))
	mux.Handle("PUT /functions/{name}/egress", adminOnly(http.HandlerFunc(functionsHandler.SetEgress)))

	// Private networks (admin only).
	mux.Handle("GET /networks", adminOnly(http.HandlerFunc(networksHandler.List)))
	mux.Handle("POST /networks", adminOnly(http.HandlerFunc(networksHandler.Create)))
	mux.Handle("GET /networks/config", adminOnly(http.HandlerFunc(networksHandler.Config)))
	mux.Handle("GET /networks/docker", adminOnly(http.HandlerFunc(networksHandler.ListDocker)))
	mux.Handle("GET /networks/docker/{id}", adminOnly(http.HandlerFunc(networksHandler.GetDocker)))
	mux.Handle("GET /networks/{name}", adminOnly(http.HandlerFunc(networksHandler.Get)))
	mux.Handle("PATCH /networks/{name}", adminOnly(http.HandlerFunc(networksHandler.Update)))
	mux.Handle("DELETE /networks/{name}", adminOnly(http.HandlerFunc(networksHandler.Delete)))
	mux.Handle("POST /networks/{name}/members", adminOnly(http.HandlerFunc(networksHandler.Attach)))
	mux.Handle("DELETE /networks/{name}/members/{kind}/{member}", adminOnly(http.HandlerFunc(networksHandler.Detach)))
	mux.Handle("POST /networks/{name}/disconnect", adminOnly(http.HandlerFunc(networksHandler.Disconnect)))

	// Audit trail (admins, read-only: no write route)
	auditPath, err := audit.FilePath(cfg.LogDir)
	if err != nil {
		logger.Error("audit trail: resolving the log path", "error", err)
		os.Exit(1)
	}
	auditHandler := &handler.AuditHandler{Reader: &audit.Reader{Path: auditPath}, Audit: auditLogger, Logger: logger}
	mux.Handle("GET /audit", adminOnly(http.HandlerFunc(auditHandler.List)))

	// WebSocket (authenticated)
	mux.Handle("GET /ws", authenticated(wsHandler))

	// Apply global middlewares
	global := middleware.Chain(mux,
		middleware.Recovery(logger),
		middleware.Logging(logger),
		// Before RateLimit, so that rate-limited clients are audited too.
		middleware.AuditRequests(auditLogger),
		middleware.RateLimit(120, time.Minute),
		middleware.SecurityHeaders(),
		middleware.CORS(cfg.FrontURL),
		// CSRF: browser writes must come from the frontend (after CORS, so
		// that the frontend can read a refusal). CLI and MCP send no Origin.
		middleware.SameOrigin(cfg.FrontURL),
	)

	// Configure the HTTP server
	server := &http.Server{
		Addr:              fmt.Sprintf(":%d", cfg.Port),
		Handler:           global,
		ReadHeaderTimeout: 15 * time.Second, // Slowloris guard on headers only
		ReadTimeout:       0,                // Disabled for streaming uploads (cp)
		WriteTimeout:      0,                // Disabled for WebSocket and streaming
		IdleTimeout:       60 * time.Second,
	}

	// Graceful shutdown. main waits for it: the aggregated invocation
	// entries are written once the last request has ended.
	shutdownDone := make(chan struct{})
	go func() {
		defer close(shutdownDone)
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		sig := <-sigCh
		logger.Info("signal received, shutting down", "signal", sig)

		cronScheduler.Stop()
		stopServices()

		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		if err := server.Shutdown(shutdownCtx); err != nil {
			logger.Error("server shutdown error", "error", err)
		}
		if functionsHandler.FlushAudit != nil {
			functionsHandler.FlushAudit()
		}
	}()

	logger.Info("server listening", "addr", server.Addr)
	if err := server.ListenAndServe(); err != http.ErrServerClosed {
		logger.Error("server error", "error", err)
		os.Exit(1)
	}

	<-shutdownDone
	logger.Info("server stopped gracefully")
}
