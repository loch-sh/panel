# web

Full web hosting server: the panel with its hosting, SFTP and services plugins, SFTPGo for the customers' files, and [deploy-server](https://github.com/loch-sh/deploy) for GitLab CI deployments. The common design is in the [examples README](../README.md).

| Service | Role | Public address |
|---|---|---|
| `traefik` | Reverse proxy, Let's Encrypt, HTTP → HTTPS | `:80`, `:443` (TCP + UDP/HTTP3) |
| `api` | Panel API | `https://manager.api.<DOMAIN>` |
| `interface` | Panel web interface | `https://manager.<DOMAIN>` |
| `deploy` | GitLab CI deployments of Compose stacks | `https://deploy.<DOMAIN>` |
| `sftpgo` | SFTP server and web client of the hosted sites | `sftp://ftp.<DOMAIN>:2022`, `https://ftp.<DOMAIN>/web/client` |

Databases, caches and queues are not in this file: they are [managed services](../../host-panel-api/docs/services.md) created from the panel (one instance per engine version, or one container per resource for Redis, Valkey, NATS, Loch S3…), in `DATA_ROOT/compose/svc-*`.

## Configuration

`.env` (copy `.env.example`):

| Variable | Description |
|---|---|
| `DOMAIN` | Base domain (default `127.0.0.1.traefik.me`: [local run](../README.md#local-run)) |
| `ACME_EMAIL` | E-mail of the Let's Encrypt account |
| `DATA_ROOT` | Root of the host directories (default `/data`; `./data` on macOS when unset), see [Local run](../README.md#local-run) |
| `GITLAB_HOST` | GitLab instance issuing the CI ID tokens (`gitlab.com` or your own) |
| `DEPLOY_NAMESPACES` | GitLab groups allowed to deploy |
| `DEPLOY_ALLOWED_IPS` | IPs/CIDRs of the CI runners |

`app.env` (copy `app.env.example`): fill in the OIDC client (`CLIENT_ID`, `CLIENT_SECRET` and the provider's variables; callback `https://manager.api.<DOMAIN>/openid-connect/redirect`), `COOKIE_SECRET` and `PASSWORD_SECRET` (`openssl rand -hex 32`), and `SFTP_API_KEY` (see below). Every variable is described in [configuration](../../host-panel-api/docs/configuration.md), [hosting](../../host-panel-api/docs/hosting.md#configuration), [SFTP](../../host-panel-api/docs/sftp.md) and [services](../../host-panel-api/docs/services.md#configuration).

## Installation

Prerequisites: Docker with the Compose plugin, ports 80, 443 and 2022 open, DNS records of `manager`, `manager.api`, `deploy` and `ftp` under `<DOMAIN>` pointing to the host (none for a [local run](../README.md#local-run) with the default `127.0.0.1.traefik.me`). To test a change of the panel in this stack, see [web-build](../web-build).

```bash
# 1. Host directories (sites belong to 1000:1000, the user of SFTPGo and HOSTING_RUN_UID)
sudo mkdir -p /data/{traefik/letsencrypt,traefik/logs,panel/data,panel/log,compose,websites/hosted,services,deploy} \
  /data/sftpgo/{data,logs,config}
sudo chown -R 1000:1000 /data/sftpgo /data/websites/hosted

# 2. Configuration
cp .env.example .env && cp app.env.example app.env   # then edit both

# 3. SFTPGo admin and API key (SFTP_API_KEY), required by the SFTP plugin
./up.sh sftpgo
ssh -L 8081:127.0.0.1:8081 <host>                   # from your machine
# open http://localhost:8081/web/admin and create the admin.
# Then either run ./up.sh (step 4): with SFTP_API_KEY empty it asks
# "Create a new SFTPGo admin API key now? [y/N]", then the admin's username
# and password, enables "API key authentication" on its profile, creates the
# key and writes it to app.env. Or by hand: enable "API key authentication"
# in the admin's profile; the web admin has no API key page, create the key
# with the REST API (curl asks for the admin password)
ADMIN=admin                                         # the admin you created
TOKEN=$(curl -su "$ADMIN" http://127.0.0.1:8081/api/v2/token | sed -E 's/.*"access_token":"([^"]+)".*/\1/')
curl -s -X POST http://127.0.0.1:8081/api/v2/apikeys \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d "{\"name\":\"loch-panel\",\"scope\":1,\"admin\":\"$ADMIN\"}"
# the "key" field is shown only once: set it as SFTP_API_KEY in app.env

# 4. Start
./up.sh
docker compose logs -f api
```

`up.sh` creates the shared `traefik_default` network if it is missing (`docker network create traefik_default`), then runs `docker compose up -d` with its arguments and prints the URLs of the stack. With `SFTP_ENABLED=true` and an empty `SFTP_API_KEY`, the API would refuse to start: `up.sh` starts SFTPGo only and, in a terminal, offers to create the key (the password is read without echo and passed to curl on its stdin, never in the process arguments); without a terminal, or on "no", it prints the manual steps and exits with code 1. The network stays `external` in `compose.yaml`: hosted sites and CI stacks use it too, so `docker compose down` never removes it.

The API refuses to start while a required value is missing (the logs list them). The services module creates the `loch-services` network at startup and pulls the engine images on first use.

## Networks

```
traefik_default   external (created by up.sh), subnet from the daemon's default-address-pools
  traefik, hosted sites, CI stacks

loch-panel        10.255.252.0/29
  traefik        10.255.252.2   (= TRUSTED_PROXIES of api)
  api            10.255.252.3
  interface      10.255.252.4

loch-deploy       10.255.250.0/29
  traefik        10.255.250.2   (= TRUSTED_PROXIES of deploy)
  deploy         10.255.250.3

loch-sftpgo       10.255.251.0/29
  traefik        10.255.251.2   (= PROXY_ALLOWED of sftpgo)
  sftpgo         10.255.251.3
  api            10.255.251.4   (SFTP_API_URL=http://sftpgo:8081)

loch-services     created by the api (internal), managed service instances and the sites using them
loch-net-*        private networks created in the panel
```

- No container of `traefik_default` can reach the API, deploy-server or the SFTPGo admin API directly.
- Never add `loch-panel`, `loch-deploy` or `loch-sftpgo` to `ALLOWED_EXTERNAL_NETWORKS` of deploy-server.

## Deploying from GitLab CI

deploy-server authenticates each job with its GitLab OIDC ID token (`aud` = `https://deploy.<DOMAIN>`) and runs `docker compose` in `DATA_ROOT/compose`, the panel's `HOSTING_STACKS_DIRECTORY`: CI stacks appear in the panel next to the hosted sites. The `.gitlab-ci.yml` template and the deployment policy are in the [deploy-server README](https://github.com/loch-sh/deploy#gitlab-ci-integration).

## Security

- **SFTPGo admin**: the admin interface and the REST API are on binding 1 only, published on `127.0.0.1:8081` (SSH tunnel to reach it); the API uses the `loch-sftpgo` network. The public binding (`ftp.<DOMAIN>`) serves the web client only.
- **deploy-server** mounts the whole `websites/` directory (its `WEBSITES_BASE`), `websites/hosted/` included: a CI job of an allowed GitLab group can write into the hosted sites' files. Keep `DEPLOY_NAMESPACES` narrow.
- **Session cookie**: leave `COOKIE_DOMAIN` unset. Set to `.<DOMAIN>`, the admin session would be sent to every hosted site under `DOMAIN`.
- **SFTP**: port 2022 is public; the SFTPGo defender bans IPs after 10 failed attempts (30 min).
- See also the [common security notes](../README.md#security).
