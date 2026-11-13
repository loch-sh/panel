# light

The panel and [deploy-server](https://github.com/loch-sh/deploy): stacks are deployed by GitLab CI, the panel manages and monitors them. No SFTPGo, and the panel plugins (hosting, services, SFTP) are off. The common design is in the [examples README](../README.md); the full version is [web](../web).

| Service | Role | Public address |
|---|---|---|
| `traefik` | Reverse proxy, Let's Encrypt, HTTP → HTTPS | `:80`, `:443` (TCP + UDP/HTTP3) |
| `api` | Panel API | `https://manager.api.<DOMAIN>` |
| `interface` | Panel web interface | `https://manager.<DOMAIN>` |
| `deploy` | GitLab CI deployments of Compose stacks | `https://deploy.<DOMAIN>` |

## Configuration

`.env` (copy `.env.example`): `DOMAIN` (default `127.0.0.1.traefik.me`: [local run](../README.md#local-run)), `ACME_EMAIL`, `DATA_ROOT` (default `/data`; `./data` on macOS when unset), and for deploy-server `GITLAB_HOST`, `DEPLOY_NAMESPACES` (GitLab groups allowed to deploy), `DEPLOY_ALLOWED_IPS` (CI runners).

`app.env` (copy `app.env.example`): the OIDC client (callback `https://manager.api.<DOMAIN>/openid-connect/redirect`), `COOKIE_SECRET` and `PASSWORD_SECRET` (`openssl rand -hex 32`). See [configuration](../../host-panel-api/docs/configuration.md).

To enable the hosting or services plugin, copy its block from [`../web/app.env.example`](../web/app.env.example) and mount its directories in the `api` service at the same path, as in [`../web/compose.yaml`](../web/compose.yaml) (`HOSTING_STACKS_DIRECTORY` = deploy-server's `COMPOSE_BASE`, so that CI stacks and hosted sites share one directory).

## Installation

Prerequisites: Docker with the Compose plugin, ports 80 and 443 open, DNS records of `manager`, `manager.api` and `deploy` under `<DOMAIN>` pointing to the host (none for a [local run](../README.md#local-run) with the default `127.0.0.1.traefik.me`).

```bash
sudo mkdir -p /data/{traefik/letsencrypt,traefik/logs,panel/data,panel/log,compose,websites,deploy}
cp .env.example .env && cp app.env.example app.env   # then edit both
./up.sh
docker compose logs -f api
```

`up.sh` creates the shared `traefik_default` network if it is missing, then runs `docker compose up -d` with its arguments and prints the URLs of the stack. The network stays `external` in `compose.yaml` (shared with the other routed stacks): `docker compose down` never removes it.

## Networks

```
traefik_default   external (created by up.sh): traefik, CI stacks
loch-panel        10.255.252.0/29: traefik .2 (= TRUSTED_PROXIES of api), api .3, interface .4
loch-deploy       10.255.250.0/29: traefik .2 (= TRUSTED_PROXIES of deploy), deploy .3
```

Never add `loch-panel` or `loch-deploy` to `ALLOWED_EXTERNAL_NETWORKS` of deploy-server. The `.gitlab-ci.yml` template is in the [deploy-server README](https://github.com/loch-sh/deploy#gitlab-ci-integration).
