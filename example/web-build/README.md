# web-build

The [web](../web) example with the panel built from the local sources: test a change of the API or the interface in a full stack (Traefik, hosting, SFTPGo, deploy-server, managed services) before a release. Not for production: use [web](../web), with the released images.

| Service | Image |
|---|---|
| `api` | built from [`../../host-panel-api`](../../host-panel-api) (`loch-panel/api:local`) |
| `interface` | built from [`../../host-panel-interface`](../../host-panel-interface) (`loch-panel/interface:local`) |
| `traefik`, `deploy`, `sftpgo` | the same released images as web |

Everything else is identical to web: `compose.yaml` differs only by the `build` of these two services, `.env.example`, `app.env.example` and `up.sh` are the same files. Configuration, installation, networks and security: see the [web README](../web/README.md).

## Usage

```bash
cd example/web-build
cp .env.example .env && cp app.env.example app.env   # then edit both (OIDC client, secrets)
./up.sh                                              # builds the API and the interface, then starts the stack
```

Both services have `pull_policy: build`: every `./up.sh` (or `docker compose up`) rebuilds them from the current sources, with the Docker layer cache. After a change:

```bash
./up.sh api          # rebuild and recreate the API only
./up.sh interface    # the interface only
./up.sh              # both, and the rest of the stack
```

The project name (`loch`), the networks and `DATA_ROOT` are those of web: switching between web and web-build keeps the users, sites and service instances. Only one of them runs at a time.

By default `DOMAIN` is `127.0.0.1.traefik.me` (hosts resolved to `127.0.0.1`, self-signed certificate) and, on macOS, `DATA_ROOT` is `./data`: see [Local run](../README.md#local-run).
