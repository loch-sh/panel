package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"host-panel-api/internal/env"
)

// setRequiredEnv sets the minimum environment variables needed for a valid config.
func setRequiredEnv(t *testing.T, provider string) {
	t.Helper()
	t.Setenv("OIDC_PROVIDER", provider)
	t.Setenv("CLIENT_ID", "test-client-id")
	t.Setenv("CLIENT_SECRET", "test-client-secret")
	t.Setenv("REDIRECT_URI", "http://localhost:2538/callback")
	t.Setenv("SCOPE", "openid profile email")
	t.Setenv("FRONT_URL", "http://localhost:5173")
	t.Setenv("COOKIE_SECRET", "this-is-a-very-long-cookie-secret-for-testing!")
	t.Setenv("PASSWORD_SECRET", "password-secret")
}

func TestConfig_Load_Defaults(t *testing.T) {
	setRequiredEnv(t, "microsoft")
	t.Setenv("TENANT_ID", "test-tenant")
	t.Setenv("APPLICATION_ID", "test-app")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if cfg.Port != 2538 {
		t.Errorf("Port = %d, want 2538", cfg.Port)
	}
	if cfg.DataDir != "./data" {
		t.Errorf("DataDir = %q, want %q", cfg.DataDir, "./data")
	}
	if cfg.Env != "production" {
		t.Errorf("Env = %q, want %q", cfg.Env, "production")
	}
	if cfg.CronHTTPTimeout != 900 {
		t.Errorf("CronHTTPTimeout = %d, want 900", cfg.CronHTTPTimeout)
	}
	if cfg.CronMaxJobs != 50 {
		t.Errorf("CronMaxJobs = %d, want 50", cfg.CronMaxJobs)
	}
}

func TestConfig_Load_CustomPort(t *testing.T) {
	setRequiredEnv(t, "microsoft")
	t.Setenv("TENANT_ID", "test-tenant")
	t.Setenv("APPLICATION_ID", "test-app")
	t.Setenv("PORT", "8080")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.Port != 8080 {
		t.Errorf("Port = %d, want 8080", cfg.Port)
	}
}

func TestConfig_Load_InvalidPort(t *testing.T) {
	setRequiredEnv(t, "microsoft")
	t.Setenv("TENANT_ID", "test-tenant")
	t.Setenv("APPLICATION_ID", "test-app")
	t.Setenv("PORT", "not-a-number")

	_, err := Load()
	if err == nil {
		t.Fatal("expected error for invalid PORT")
	}
	if !strings.Contains(err.Error(), "PORT") {
		t.Errorf("error should mention PORT: %v", err)
	}
}

func TestConfig_Validate_MissingVars(t *testing.T) {
	// Set only the provider, missing all required vars
	t.Setenv("OIDC_PROVIDER", "microsoft")
	t.Setenv("TENANT_ID", "test-tenant")
	t.Setenv("APPLICATION_ID", "test-app")

	_, err := Load()
	if err == nil {
		t.Fatal("expected error for missing required vars")
	}
	problems, ok := env.AsProblems(err)
	if !ok || len(problems) != 7 || !strings.Contains(err.Error(), "CLIENT_ID: required") {
		t.Errorf("every missing variable is reported at once: %v", err)
	}
}

func TestConfig_Validate_ShortCookieSecret(t *testing.T) {
	setRequiredEnv(t, "microsoft")
	t.Setenv("TENANT_ID", "test-tenant")
	t.Setenv("APPLICATION_ID", "test-app")
	t.Setenv("COOKIE_SECRET", "too-short")

	_, err := Load()
	if err == nil {
		t.Fatal("expected error for short COOKIE_SECRET")
	}
	if !strings.Contains(err.Error(), "COOKIE_SECRET") {
		t.Errorf("error should mention COOKIE_SECRET: %v", err)
	}
}

func TestConfig_Validate_UnknownProvider(t *testing.T) {
	setRequiredEnv(t, "unknown-provider")

	_, err := Load()
	if err == nil {
		t.Fatal("expected error for unknown provider")
	}
	problems, _ := env.AsProblems(err)
	if len(problems) != 1 || problems[0].Var != "OIDC_PROVIDER" {
		t.Errorf("only the provider is reported: %v", err)
	}
}

func TestConfig_Validate_Keycloak(t *testing.T) {
	setRequiredEnv(t, "keycloak")
	t.Setenv("KEYCLOAK_URL", "https://keycloak.example.com")
	t.Setenv("KEYCLOAK_REALM", "myrealm")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.OIDCProvider != "keycloak" {
		t.Errorf("OIDCProvider = %q, want %q", cfg.OIDCProvider, "keycloak")
	}
}

func TestConfig_Validate_KeycloakMissingURL(t *testing.T) {
	setRequiredEnv(t, "keycloak")
	// Missing KEYCLOAK_URL and KEYCLOAK_REALM

	_, err := Load()
	if err == nil {
		t.Fatal("expected error for missing keycloak vars")
	}
}

func TestConfig_Validate_Generic(t *testing.T) {
	setRequiredEnv(t, "generic")
	t.Setenv("OIDC_DISCOVERY_URL", "https://idp.example.com/.well-known/openid-configuration")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.OIDCProvider != "generic" {
		t.Errorf("OIDCProvider = %q, want %q", cfg.OIDCProvider, "generic")
	}
}

func TestConfig_Validate_GenericMissingDiscoveryURL(t *testing.T) {
	setRequiredEnv(t, "generic")
	// Missing OIDC_DISCOVERY_URL

	_, err := Load()
	if err == nil {
		t.Fatal("expected error for missing OIDC_DISCOVERY_URL")
	}
}

func TestConfig_SFTP_DisabledByDefault(t *testing.T) {
	setRequiredEnv(t, "microsoft")
	t.Setenv("TENANT_ID", "test-tenant")
	t.Setenv("APPLICATION_ID", "test-app")
	t.Setenv("SFTP_ENABLED", "false")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.SFTPEnabled {
		t.Error("SFTPEnabled should be false")
	}
}

func TestConfig_SFTP_EnabledRequiresVars(t *testing.T) {
	setRequiredEnv(t, "microsoft")
	t.Setenv("TENANT_ID", "test-tenant")
	t.Setenv("APPLICATION_ID", "test-app")
	t.Setenv("SFTP_ENABLED", "true")
	// SFTP_DIRECTORY / SFTP_API_URL / SFTP_API_KEY intentionally missing

	_, err := Load()
	if err == nil {
		t.Fatal("expected error for missing SFTP vars when enabled")
	}
	if !strings.Contains(err.Error(), "SFTP_DIRECTORY") {
		t.Errorf("error should mention SFTP_DIRECTORY: %v", err)
	}
}

func TestConfig_SFTP_EnabledValid(t *testing.T) {
	setRequiredEnv(t, "microsoft")
	t.Setenv("TENANT_ID", "test-tenant")
	t.Setenv("APPLICATION_ID", "test-app")
	t.Setenv("SFTP_ENABLED", "true")
	t.Setenv("SFTP_DIRECTORY", "/data/websites/hosted")
	t.Setenv("SFTP_API_URL", "http://sftpgo:8080")
	t.Setenv("SFTP_API_KEY", "admin-key")
	t.Setenv("SFTP_DOMAIN", "sftp.example.com")
	t.Setenv("SFTP_PORT", "2222")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if !cfg.SFTPEnabled {
		t.Error("SFTPEnabled should be true")
	}
	if cfg.SFTPDirectory != "/data/websites/hosted" {
		t.Errorf("SFTPDirectory = %q", cfg.SFTPDirectory)
	}
	if cfg.SFTPAPIURL != "http://sftpgo:8080" {
		t.Errorf("SFTPAPIURL = %q", cfg.SFTPAPIURL)
	}
}

func TestConfig_Load_InvalidCronHTTPTimeout(t *testing.T) {
	setRequiredEnv(t, "microsoft")
	t.Setenv("TENANT_ID", "test-tenant")
	t.Setenv("APPLICATION_ID", "test-app")
	t.Setenv("CRON_HTTP_TIMEOUT", "not-a-number")

	_, err := Load()
	if err == nil {
		t.Fatal("expected error for invalid CRON_HTTP_TIMEOUT")
	}
}

func TestResolveHostingMax(t *testing.T) {
	const gib = int64(1 << 30)
	tests := []struct {
		name           string
		maxMem, defMem int
		maxCPU, defCPU float64
		wantMem        int
		wantCPU        float64
		wantCapped     int
		wantErr        bool
	}{
		{name: "auto uses the host", wantMem: 8192, wantCPU: 4},
		{name: "lower max kept", maxMem: 2048, maxCPU: 2, wantMem: 2048, wantCPU: 2},
		{name: "max above host capped", maxMem: 65536, maxCPU: 16, wantMem: 8192, wantCPU: 4, wantCapped: 2},
		{name: "default within max", maxMem: 2048, defMem: 512, defCPU: 1, wantMem: 2048, wantCPU: 4},
		{name: "default above max", maxMem: 1024, defMem: 2048, wantErr: true},
		{name: "default cpu above host", defCPU: 8, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &Config{
				HostingMaxMemoryMB: tt.maxMem, HostingDefaultMemoryMB: tt.defMem,
				HostingMaxCPUs: tt.maxCPU, HostingDefaultCPUs: tt.defCPU,
			}
			capped, err := c.ResolveHostingMax(4, 8*gib)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if c.HostingMaxMemoryMB != tt.wantMem || c.HostingMaxCPUs != tt.wantCPU || len(capped) != tt.wantCapped {
				t.Fatalf("got mem=%d cpus=%g capped=%v", c.HostingMaxMemoryMB, c.HostingMaxCPUs, capped)
			}
		})
	}
}

func TestConfig_Load_Services(t *testing.T) {
	setRequiredEnv(t, "microsoft")
	t.Setenv("TENANT_ID", "test-tenant")
	t.Setenv("APPLICATION_ID", "test-app")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ServicesEnabled || cfg.ServicesDataDir != "/data/services" || cfg.ServicesNetwork != "loch-services" ||
		cfg.ServicesMaxResources != 50 || len(cfg.ServicesEngines) != 0 || len(cfg.ServicesPinnedVersions) != 0 ||
		cfg.ServicesUpdateCheckHours != 12 {
		t.Fatalf("defaults = %+v", cfg)
	}

	t.Setenv("SERVICES_ENABLED", "true")
	t.Setenv("SERVICES_ENGINES", "postgres, mariadb")
	t.Setenv("SERVICES_POSTGRES_PINNED_VERSIONS", "16,17")
	t.Setenv("SERVICES_UPDATE_CHECK_INTERVAL", "0")
	t.Setenv("SERVICES_DATA_DIRECTORY", "/srv/services/")
	if cfg, err = Load(); err != nil {
		t.Fatal(err)
	}
	if !cfg.ServicesEnabled || cfg.ServicesDataDir != "/srv/services" || len(cfg.ServicesEngines) != 2 ||
		len(cfg.ServicesPinnedVersions["postgres"]) != 2 || cfg.ServicesUpdateCheckHours != 0 {
		t.Fatalf("cfg = %+v", cfg)
	}

	for env, value := range map[string]string{
		"SERVICES_ENABLED":                    "maybe",
		"SERVICES_MAX_RESOURCES_PER_INSTANCE": "0",
		"SERVICES_DATA_DIRECTORY":             "relative/path",
		"SERVICES_POSTGRES_VERSIONS":          "16,17", // removed: versions are discovered
		"SERVICES_UPDATE_CHECK_INTERVAL":      "200",
		"SERVICES_ARCHIVE_ON_RETIRE":          "perhaps",
		"SERVICES_NETWORK":                    "host",
		"SERVICES_TLS":                        "maybe",
		"SERVICES_PUBLIC_HOST":                "https://db.example.com/",
		"SERVICES_TLS_CERT_FILE":              "/etc/cert.pem", // without the key
		"SERVICES_ALLOW_PUBLIC_BIND":          "sure",
		"HOSTING_SITES_DIRECTORY":             "/srv/services/sites", // contains the data
		"SFTP_DIRECTORY":                      "/srv",                // contains the data
	} {
		t.Run(env, func(t *testing.T) {
			t.Setenv(env, value)
			if _, err := Load(); err == nil {
				t.Fatalf("%s=%q must be refused", env, value)
			}
		})
	}
}

func TestConfig_Lint_UnknownAndRemoved(t *testing.T) {
	setRequiredEnv(t, "microsoft")
	t.Setenv("TENANT_ID", "test-tenant")
	t.Setenv("APPLICATION_ID", "test-app")
	t.Setenv("SERVICES_ENABLD", "true")
	t.Setenv("SERVICES_EXPOSED_PORTS", "postgres-17=5417")
	t.Setenv("SERVICES_EXPOSE_ALLOWED_CIDRS", "0.0.0.0/0")
	_, err := Load()
	msg := ""
	if err != nil {
		msg = err.Error()
	}
	for _, want := range []string{
		"SERVICES_ENABLD: unknown variable (did you mean SERVICES_ENABLED?)",
		"SERVICES_EXPOSED_PORTS: no longer supported",
		"SERVICES_EXPOSE_ALLOWED_CIDRS:",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("missing %q in %v", want, err)
		}
	}
}

func TestConfig_Networks(t *testing.T) {
	setRequiredEnv(t, "microsoft")
	t.Setenv("TENANT_ID", "test-tenant")
	t.Setenv("APPLICATION_ID", "test-app")
	t.Setenv("TRUSTED_PROXIES", "172.18.0.0/16, 10.0.0.1")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.NetworksMax != 50 || len(cfg.TrustedProxies) != 2 || cfg.TrustedProxies[1].Bits() != 32 {
		t.Fatalf("cfg = %+v", cfg)
	}
	t.Setenv("NETWORKS_MAX", "0")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "NETWORKS_MAX") {
		t.Fatalf("err = %v", err)
	}
}

func TestConfig_TrustedProxiesTooWide(t *testing.T) {
	setRequiredEnv(t, "microsoft")
	t.Setenv("TENANT_ID", "test-tenant")
	t.Setenv("APPLICATION_ID", "test-app")
	t.Setenv("TRUSTED_PROXIES", "0.0.0.0/0")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "TRUSTED_PROXIES") {
		t.Fatalf("err = %v", err)
	}
}

func TestConfig_ToolingVariablesAreKnown(t *testing.T) {
	setRequiredEnv(t, "microsoft")
	t.Setenv("TENANT_ID", "test-tenant")
	t.Setenv("APPLICATION_ID", "test-app")
	t.Setenv("SFTP_KEY_NAME", "host-panel") // exported by the Makefile
	if _, err := Load(); err != nil {
		t.Fatal(err)
	}
}

func TestConfig_NetworksReservedRanges(t *testing.T) {
	setRequiredEnv(t, "microsoft")
	t.Setenv("TENANT_ID", "test-tenant")
	t.Setenv("APPLICATION_ID", "test-app")
	t.Setenv("NETWORKS_RESERVED_RANGES", "192.168.1.0/24, 10.8.0.0/16")
	cfg, err := Load()
	if err != nil || len(cfg.NetworksReservedRanges) != 2 {
		t.Fatalf("%v %v", cfg, err)
	}
	for _, bad := range []string{"0.0.0.0/0", "fd00::/8", "nope"} {
		t.Setenv("NETWORKS_RESERVED_RANGES", bad)
		if _, err := Load(); err == nil || !strings.Contains(err.Error(), "NETWORKS_RESERVED_RANGES") {
			t.Errorf("%s: %v", bad, err)
		}
	}
}

func TestConfig_Load_Functions(t *testing.T) {
	setRequiredEnv(t, "microsoft")
	t.Setenv("TENANT_ID", "test-tenant")
	t.Setenv("APPLICATION_ID", "test-app")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.FunctionsEnabled || cfg.FunctionsDataDir != "/data/functions" || cfg.FunctionsMax != 50 ||
		cfg.FunctionsMaxMemoryMB != 3008 || cfg.FunctionsMaxCodeBytes != 50<<20 ||
		cfg.FunctionsDeploymentsKept != 3 || cfg.FunctionsTmpSizeMB != 512 {
		t.Fatalf("defaults = %+v", cfg)
	}

	t.Setenv("FUNCTIONS_ENABLED", "true")
	t.Setenv("FUNCTIONS_DATA_DIRECTORY", "/srv/functions/")
	t.Setenv("FUNCTIONS_MAX_MEMORY", "2G")
	t.Setenv("FUNCTIONS_MAX_CODE_SIZE", "20MiB")
	t.Setenv("FUNCTIONS_TMP_SIZE", "1024")
	t.Setenv("FUNCTIONS_MAX_TOTAL_MEMORY", "16G")
	t.Setenv("FUNCTIONS_MAX_TOTAL_CPUS", "6.5")
	if cfg, err = Load(); err != nil {
		t.Fatal(err)
	}
	if cfg.FunctionsMaxTotalMemoryMB != 16<<10 || cfg.FunctionsMaxTotalCPUs != 6.5 {
		t.Fatalf("budget = %d MB, %g CPUs", cfg.FunctionsMaxTotalMemoryMB, cfg.FunctionsMaxTotalCPUs)
	}
	f := cfg.Functions()
	if !f.Enabled || f.DataDir != "/srv/functions" || f.MaxMemoryMB != 2048 || f.MaxCodeBytes != 20<<20 ||
		f.TmpSizeMB != 1024 || f.StacksDir != "/data/compose" {
		t.Fatalf("functions = %+v", f)
	}

	for env, value := range map[string]string{
		"FUNCTIONS_ENABLED":          "maybe",
		"FUNCTIONS_DATA_DIRECTORY":   "relative/path",
		"FUNCTIONS_MAX":              "0",
		"FUNCTIONS_MAX_MEMORY":       "64",  // below Lambda's minimum
		"FUNCTIONS_MAX_CODE_SIZE":    "1T",  // unknown unit
		"FUNCTIONS_DEPLOYMENTS_KEPT": "0",   // the active one is always kept
		"FUNCTIONS_TMP_SIZE":         "20G", // above the bound
		"HOSTING_STACKS_DIRECTORY":   "/srv/functions/stacks",
		"HOSTING_SITES_DIRECTORY":    "/srv",
		"SERVICES_DATA_DIRECTORY":    "/srv/functions",
		"FUNCTIONS_MAXX":             "3", // typo of a linted prefix
		"FUNCTIONS_MAX_TOTAL_MEMORY": "64",
		"FUNCTIONS_MAX_TOTAL_CPUS":   "-1",
	} {
		t.Run(env, func(t *testing.T) {
			t.Setenv(env, value)
			if _, err := Load(); err == nil {
				t.Fatalf("%s=%q must be refused", env, value)
			}
		})
	}
	t.Run("interpolation in the data directory", func(t *testing.T) {
		t.Setenv("FUNCTIONS_DATA_DIRECTORY", "/srv/fn${HOME}")
		if _, err := Load(); err == nil || !strings.Contains(err.Error(), "FUNCTIONS_DATA_DIRECTORY: must be an absolute, clean path of letters") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("logs", func(t *testing.T) {
		t.Setenv("API_LOG_DIR", "/srv/functions/panel")
		if _, err := Load(); err == nil || !strings.Contains(err.Error(), "must not be inside API_LOG_DIR") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("panel data", func(t *testing.T) {
		// DATA_DIR itself, or inside the functions directory: refused.
		for _, dir := range []string{"/srv/functions", "/srv/functions/panel"} {
			t.Setenv("DATA_DIR", dir)
			if _, err := Load(); err == nil || !strings.Contains(err.Error(), "must not be DATA_DIR or contain it") {
				t.Fatalf("DATA_DIR=%s: err = %v", dir, err)
			}
		}
		// A subdirectory of DATA_DIR (the panel's private data): accepted.
		t.Setenv("DATA_DIR", "/srv")
		if _, err := Load(); err != nil {
			t.Fatalf("functions inside DATA_DIR refused: %v", err)
		}
	})
	t.Run("overlap through a symlink", func(t *testing.T) {
		root := t.TempDir()
		real := filepath.Join(root, "compose")
		os.MkdirAll(filepath.Join(real, "functions"), 0o755)
		os.Symlink(filepath.Join(real, "functions"), filepath.Join(root, "fn"))
		t.Setenv("HOSTING_STACKS_DIRECTORY", real)
		t.Setenv("FUNCTIONS_DATA_DIRECTORY", filepath.Join(root, "fn"))
		if _, err := Load(); err == nil || !strings.Contains(err.Error(), "must not be inside HOSTING_STACKS_DIRECTORY") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("not yet created directory under a symlink", func(t *testing.T) {
		root := t.TempDir()
		stacks := filepath.Join(root, "compose")
		os.MkdirAll(stacks, 0o755)
		os.Symlink(stacks, filepath.Join(root, "link"))
		t.Setenv("HOSTING_STACKS_DIRECTORY", stacks)
		t.Setenv("FUNCTIONS_DATA_DIRECTORY", filepath.Join(root, "link", "not", "yet"))
		if _, err := Load(); err == nil || !strings.Contains(err.Error(), "must not be inside HOSTING_STACKS_DIRECTORY") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("stacks directory inside the data directory", func(t *testing.T) {
		t.Setenv("HOSTING_STACKS_DIRECTORY", "/srv/functions/compose")
		_, err := Load()
		if err == nil || !strings.Contains(err.Error(), "FUNCTIONS_DATA_DIRECTORY: must not be inside HOSTING_STACKS_DIRECTORY") {
			t.Fatalf("err = %v", err)
		}
	})
}
