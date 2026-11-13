# Production examples

Docker Compose stacks to run Loch.sh Panel on a server, behind Traefik, with the images published by this repository's [release workflow](../.github/workflows/release.yml): `ghcr.io/loch-sh/panel/api` and `ghcr.io/loch-sh/panel/interface`. The CI checks every file of this folder with `docker compose config` ([checks.yml](../.github/workflows/checks.yml), job `examples`).

| Example | Services | Use it when |
|---|---|---|
| [web](web) | Traefik, panel (hosting, SFTP and services plugins on), [deploy-server](https://github.com/loch-sh/deploy), SFTPGo | Web agency hosting: sites created in the panel, files uploaded over SFTP, managed databases, and GitLab CI deployments |
| [light](light) | Traefik, panel (plugins off), deploy-server | Stacks are deployed by GitLab CI only |
| [minimal](minimal) | Traefik, panel (plugins off) | Stacks are deployed by other means (by hand, over SSH) |
| [web-build](web-build) | web, with the API and the interface built from the local sources | Testing a change of the panel in a full stack (not for production) |

Each example drops services from the previous one and keeps the same layout, files and network design; web-build is web with `build` instead of the panel images. Only one can run on a host: they use the same host ports, directories and network names.

## Layout

```
example/
├── web/
│   ├── compose.yaml      # the stack: no value to edit in it
│   ├── .env.example      # copy to .env: domain, ACME e-mail, deploy-server policy
│   ├── app.env.example   # copy to app.env: environment of the api (OIDC, secrets, plugins)
│   ├── up.sh             # creates the shared traefik_default network if missing, docker compose up -d, prints the URLs
│   └── README.md
├── light/                # same files, without SFTPGo, plugins off
├── minimal/              # same files, without deploy-server
└── web-build/            # web, API and interface built from ../host-panel-api, ../host-panel-interface
```

`.env`, `app.env`, `data/` and `branding/` are git-ignored.

## Quick start

```bash
cd example/web                      # or light, minimal, web-build
cp .env.example .env                # domain (default 127.0.0.1.traefik.me: local run), ACME e-mail…
cp app.env.example app.env          # OIDC client, secrets (openssl rand -hex 32)
./up.sh                             # creates the traefik_default network if missing, then docker compose up -d
docker compose logs -f api          # the API lists every configuration problem at startup
```

`up.sh` prints the URLs of the stack. Open `https://manager.<DOMAIN>`: the first user to log in becomes admin. Create an API key in the interface to use the [`loch` CLI](https://lab.nitrique.net/loch.sh/cli).

## Common design

- **One base domain**: every public host is a subdomain of `DOMAIN` (`manager.<DOMAIN>` for the interface, `manager.api.<DOMAIN>` for the API, `deploy.<DOMAIN>`, `ftp.<DOMAIN>`). In production, DNS records must point to the host; the default `127.0.0.1.traefik.me` is for a [local run](#local-run).
- **Every value in `.env` / `app.env`**: `compose.yaml` needs no edit. Mandatory values (`DOMAIN`, `ACME_EMAIL`, …) use `${VAR:?}`: Compose refuses to start without them. The API checks its own variables and refuses to start with the list of problems ([configuration](../host-panel-api/docs/configuration.md#startup-checks)).
- **HTTPS by default**: Traefik gets Let's Encrypt certificates from the `letsencrypt` resolver, the one the hosting plugin puts on the sites it creates. HTTP is redirected to HTTPS by a catch-all router of priority 1 (`http-to-https`, labels of the `traefik` service), not by the `web` entrypoint: an entrypoint redirection applies before the routers, so a hosted site with HTTPS off would be redirected too. The panel, deploy-server and SFTPGo have no HTTP router and fall into the catch-all; a hosted site follows its own settings (HTTPS off: served over HTTP; "Redirect to HTTPS": its own redirect). Do not add `--entryPoints.web.http.redirections…` back.
- **Panel off the shared network**: `traefik_default` is shared with every routed stack (hosted sites, CI stacks). The API, the interface, deploy-server and SFTPGo are only on dedicated `/29` networks with Traefik (`loch-panel`, `loch-deploy`, `loch-sftpgo`), where Traefik has a fixed IP. Each service trusts `X-Forwarded-For` from that IP only (`TRUSTED_PROXIES`, `PROXY_ALLOWED`): a container on a shared network could otherwise forge its client IP (rate limit, audit trail, IP allowlists).
- **Fixed subnets in `10.255.0.0/16`**, outside the daemon's default pools (`172.16.0.0/12`, `192.168.0.0/16`), so Docker never gives them to another network. Check that the host's LAN or VPN does not use them; list the ranges it uses in `NETWORKS_RESERVED_RANGES` (app.env) so no private network of the panel overlaps them.
- **Host directories under `DATA_ROOT`** (default `/data`). Directories that `docker compose` uses inside a container (`compose/`, `websites/`, `services/`) are mounted at the same path as on the host, as the API and deploy-server require.
- **Images**: Docker Hub images are pulled from `mirror.gcr.io`; ours from `ghcr.io/loch-sh`. The panel images are pinned to a release (API `v1.1.0`, interface `v1.1.1`: each project has its own releases): update the tag on each [release](https://github.com/loch-sh/panel/releases), and check the release notes first. `:latest` and `:main` exist but are not meant for production.

## Host directories

| Path (under `DATA_ROOT`) | Used by |
|---|---|
| `traefik/letsencrypt`, `traefik/logs` | Traefik: certificates (`acme.json`), logs |
| `panel/data`, `panel/log` | API: users, API keys, scheduled tasks, networks registry; audit trail (`audit.log`) |
| `compose/` | Compose projects: hosted sites and service instances (panel), CI stacks (deploy-server) |
| `websites/hosted/` | Files of the hosted sites (panel and SFTPGo) |
| `services/` | Data of the managed service instances |
| `deploy/` | deploy-server state |
| `sftpgo/` | SFTPGo database, logs, configuration |

## Security

- **Docker socket**: the API, deploy-server and Traefik mount `/var/run/docker.sock`, which is root on the host. The `:ro` flag on Traefik does not make the Docker API read-only; a filtering proxy (`tecnativa/docker-socket-proxy`) reduces what Traefik can do. See [security](../host-panel-api/docs/security.md).
- **Published ports bypass the host firewall** (`ufw`, `INPUT` chain): filter them in the `DOCKER-USER` chain. The only ports published here are 80, 443, SFTP (2022) and the SFTPGo admin on the host loopback.
- **Audit trail**: keep `panel/log` on persistent storage and copy it off the host (remote syslog, `chattr +a`) if it must survive a root compromise ([audit](../host-panel-api/docs/audit.md)).

## Local run

`.env.example` is ready for a local run:

- **`DOMAIN=127.0.0.1.traefik.me`**: [traefik.me](https://traefik.me) is a public DNS name that resolves any `<x>.127.0.0.1.traefik.me` to `127.0.0.1`, so `manager.`, `manager.api.`, `deploy.` and `ftp.127.0.0.1.traefik.me` reach the local Traefik without any DNS record or `/etc/hosts` entry. Use `<x>.<LAN IP>.traefik.me` (e.g. `192.168.1.20.traefik.me`) to reach the stack from another device of the network.
- **Certificates**: Let's Encrypt cannot validate a host that resolves to `127.0.0.1` (Traefik logs the failure): Traefik serves its self-signed certificate. Open `https://manager.api.<DOMAIN>` once and accept the certificate, then the interface: until then the browser blocks the interface's calls to the API and the page stays blank. `up.sh` reminds it.
- **OIDC**: register `https://manager.api.<DOMAIN>/openid-connect/redirect` as a redirect URI of the client (Entra ID: platform "Web", not "SPA"). `FRONT_URL` and `REDIRECT_URI` in `app.env` derive from `DOMAIN`: leave them as they are.
- **`DATA_ROOT` on macOS** (Docker Desktop, OrbStack): `/data` cannot be shared with the Docker VM. Without `DATA_ROOT` in `.env`, `up.sh` uses `./data` next to the compose file (git-ignored); set an absolute path in `.env` to choose another one.
- Docker Desktop and OrbStack mount `/var/run/docker.sock` as is. Docker Desktop shows clients with its gateway IP (often `192.168.65.1`): add it to `DEPLOY_ALLOWED_IPS` to call deploy-server locally.
- To test a change of the panel, use [web-build](web-build): the API and the interface are built from the local sources.
