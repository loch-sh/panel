# minimal

The panel behind Traefik, nothing else: stacks are deployed by other means (by hand, over SSH) and the panel plugins are off. The common design is in the [examples README](../README.md).

| Service | Role | Public address |
|---|---|---|
| `traefik` | Reverse proxy, Let's Encrypt, HTTP → HTTPS | `:80`, `:443` (TCP + UDP/HTTP3) |
| `api` | Panel API | `https://manager.api.<DOMAIN>` |
| `interface` | Panel web interface | `https://manager.<DOMAIN>` |

## Configuration

`.env` (copy `.env.example`): `DOMAIN` (default `127.0.0.1.traefik.me`: [local run](../README.md#local-run)), `ACME_EMAIL`, `DATA_ROOT` (default `/data`; `./data` on macOS when unset).

`app.env` (copy `app.env.example`): the OIDC client (callback `https://manager.api.<DOMAIN>/openid-connect/redirect`), `COOKIE_SECRET` and `PASSWORD_SECRET` (`openssl rand -hex 32`). See [configuration](../../host-panel-api/docs/configuration.md).

To enable the hosting or services plugin, copy its block from [`../web/app.env.example`](../web/app.env.example) and mount its directories in the `api` service at the same path, as in [`../web/compose.yaml`](../web/compose.yaml).

## Installation

Prerequisites: Docker with the Compose plugin, ports 80 and 443 open, DNS records of `manager` and `manager.api` under `<DOMAIN>` pointing to the host (none for a [local run](../README.md#local-run) with the default `127.0.0.1.traefik.me`).

```bash
sudo mkdir -p /data/{traefik/letsencrypt,traefik/logs,panel/data,panel/log}
cp .env.example .env && cp app.env.example app.env   # then edit both
./up.sh
docker compose logs -f api
```

`up.sh` creates the shared `traefik_default` network if it is missing, then runs `docker compose up -d` with its arguments and prints the URLs of the stack. The network stays `external` in `compose.yaml` (shared with the other routed stacks): `docker compose down` never removes it.

## Networks

```
traefik_default   external (created by up.sh): traefik, the other routed stacks
loch-panel        10.255.252.0/29: traefik .2 (= TRUSTED_PROXIES of api), api .3, interface .4
```

Without deploy-server, no policy checks the stacks started on the host: any of them can declare a Traefik router, including a `Host()` rule of the panel. Only start trusted stacks.
