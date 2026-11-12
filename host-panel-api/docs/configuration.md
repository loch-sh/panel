# Configuration

The API reads its configuration from environment variables (a `.env` file in development: copy `.env.example`; the hosting variables are not in it, see [Web hosting](hosting.md#configuration)). Every variable is checked at startup (see [Startup checks](#startup-checks)). Only the installation checks of the services module (paths, mounts, Docker, `docker compose`) disable it without stopping the server (see [Managed services](services.md#troubleshooting)).

## Core

| Variable | Description | Default |
|---|---|---|
| `FRONT_URL` | URL of the web interface: the only origin allowed by CORS, CSRF checks and the WebSocket | required |
| `COOKIE_SECRET` | Secret signing the session cookies (at least 32 characters) | required |
| `PASSWORD_SECRET` | Must be set at startup; not used by the current version | required |
| `COOKIE_DOMAIN` | Domain of the session cookies | host of the API |
| `APP_ENV` | `development` enables debug logs | `production` |
| `PORT` | Listen port | `2538` |
| `DATA_DIR` | Users, API keys, scheduled tasks (JSON files) | `./data` |
| `API_LOG_DIR` | Directory of `audit.log` (a volume in the Docker image: mount it to keep the trail) | `./log` |
| `CRON_HTTP_TIMEOUT` | Maximum duration of an HTTP scheduled task, in seconds | `900` |
| `CRON_MAX_JOBS` | Maximum number of scheduled tasks | `50` |
| `TRUSTED_PROXIES` | Reverse proxies allowed to set `X-Forwarded-For` (client IP of the rate limit and the audit trail): comma-separated CIDRs or addresses. The loopback is always trusted. Behind Traefik, set the subnet of the shared network (`docker network inspect traefik`), e.g. `172.18.0.0/16`. Before this variable, all of `172.16.0.0/12` was trusted: any container could forge its IP | loopback only |
| `NETWORKS_MAX` | Maximum number of [private networks](networks.md) (the system network does not count) | `50` |
| `NETWORKS_RESERVED_RANGES` | IPv4 ranges (at least /8) used outside Docker — LAN, VPN, static routes — that no [private network](networks.md#overlaps-and-reserved-ranges) may overlap | empty |

## Authentication (OIDC)

| Variable | Description | Default |
|---|---|---|
| `OIDC_PROVIDER` | `microsoft`, `keycloak` or `generic` | `microsoft` |
| `CLIENT_ID`, `CLIENT_SECRET` | OAuth 2.0 client | required |
| `REDIRECT_URI` | Public URL of the API (the callback is `<REDIRECT_URI>/openid-connect/redirect`) | required |
| `SCOPE` | OAuth scopes, e.g. `openid email profile` | required |

Provider-specific variables:

| Provider | Variables |
|---|---|
| Microsoft Entra ID | `TENANT_ID`, `APPLICATION_ID` |
| Keycloak | `KEYCLOAK_URL`, `KEYCLOAK_REALM` |
| Generic (Authentik, Authelia, Zitadel, Auth0, Okta, Google, GitLab…) | `OIDC_DISCOVERY_URL` |

The first user to log in becomes admin; the next ones are restricted until an admin changes their profile.

## Startup checks

Every variable is read through one linter (`internal/env`) that records every problem instead of stopping at the first, then **refuses to start** and lists them all (one log line per variable, `invalid configuration`):

- **Invalid or missing**: a wrong type or range, a required variable missing, `COOKIE_SECRET` shorter than 32 characters, a malformed list or CIDR, an invalid `SERVICES_ENGINES` or `SERVICES_<ENGINE>_PINNED_VERSIONS`, an unreadable or invalid `SERVICES_TLS_CERT_FILE` / `SERVICES_TLS_KEY_FILE`.
- **Unknown with a known prefix**: a variable starting with `OIDC_`, `SFTP_`, `HOSTING_`, `SERVICES_`, `FUNCTIONS_`, `NETWORKS_`, `CRON_`, `COOKIE_`, `KEYCLOAK_` or `TRUSTED_` that the API does not read is almost always a typo; the message suggests the closest name. Variables of the development tooling (`SFTP_KEY_NAME`, used by `make sftp-key`) are known and ignored.
- **Removed**: `SERVICES_EXPOSED_PORTS`, `SERVICES_PUBLISH_ADDRESS`, `SERVICES_TLS_OPTIONAL` (ports are chosen in the panel, see [Managed services](services.md#publishing-a-port)) and `SERVICES_<ENGINE>_VERSIONS` (versions are discovered, see [Managed services](services.md#versions-and-updates)). The message says what replaces them.

Values are never written in the messages (a variable may hold a secret). Configuration errors used to disable the services module quietly; they now stop the API.

## Optional plugins

Each plugin is off by default and has its own page:

- **Web hosting**: `HOSTING_*` variables, see [Web hosting](hosting.md#configuration).
- **SFTP users**: `SFTP_*` variables, see [SFTP users](sftp.md#configuration).
- **Managed services**: `SERVICES_*` variables, see [Managed services](services.md#configuration), including `SERVICES_EXPOSE_ALLOWED_CIDRS` and `SERVICES_ALLOW_TLS_OPTIONAL`, which bound what can be published from the panel, and `SERVICES_<ENGINE>_PINNED_VERSIONS` and `SERVICES_UPDATE_CHECK_INTERVAL`, which drive the version discovery (it needs outbound HTTPS to `registry-1.docker.io`, `auth.docker.io`, `ghcr.io`, `mirror.gcr.io` and `endoflife.date`).
- **Private networks**: always available, see [Private networks](networks.md); `NETWORKS_MAX` above.
- **Functions** (work in progress): `FUNCTIONS_*` variables, see [Functions](functions.md#configuration); the loch function runtime binary is found and downloaded on GitHub (`api.github.com`, `github.com` and its asset hosts; newest release, at startup and every hour) (`FUNCTIONS_RUNTIME_DOWNLOAD`, or `FUNCTIONS_RUNTIME_SOURCE_DIR` offline, see [Runtime binary](functions.md#runtime-binary)). A failed installation check (directories, compose, Docker host) disables the module only (`GET /functions/config` reports `FUNCTIONS_MISCONFIGURED`), like the services module. `FUNCTIONS_DATA_DIRECTORY` must be outside `HOSTING_STACKS_DIRECTORY`, `HOSTING_SITES_DIRECTORY`, `SFTP_DIRECTORY`, `SERVICES_DATA_DIRECTORY` and `API_LOG_DIR` (symlinks resolved), and may be a subdirectory of `DATA_DIR` (never `DATA_DIR` itself or a parent of it), and, like `HOSTING_STACKS_DIRECTORY` when the module is enabled, use only letters, digits and `_ . / -`.

## Images

The panel pulls the images it runs (services, hosted sites) from `mirror.gcr.io`, Google's public mirror of Docker Hub. A few hosted-site images missing from the mirror are still pulled from Docker Hub: `webdevops/php-apache` 8.4, 8.4-alpine, 8.1-alpine and 8.0-alpine, and `webdevops/nginx:alpine`. Loch S3, our own image, is pulled from `ghcr.io/loch-sh/s3` (it is not on Docker Hub).

## Paths in Docker

When the API runs in a container, the hosting and services directories must be bind-mounted **at the same path** as on the host: `docker compose` resolves paths in the API container, while the Docker daemon reads them on the host. The API checks this at startup.
