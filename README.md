# Loch.sh Panel

Self-hosted panel to manage Docker containers, hosted sites and managed services (databases, cache, messaging, storage) through a web interface, a REST/WebSocket API and the `loch` CLI.

This repository holds the API and the web interface:

| Project | Role | Stack |
|---|---|---|
| [`host-panel-api`](host-panel-api/README.md) | REST and WebSocket API (port 2538) | Go 1.27, Docker SDK |
| [`host-panel-interface`](host-panel-interface/README.md) | Web interface (port 5173), in 10 languages | React 19, TypeScript, Ant Design 6, TanStack Query |

The `loch` CLI and MCP server live in their own repository: [loch.sh/cli](https://lab.nitrique.net/loch.sh/cli).

## Run locally

Requirements: Go 1.27+, Node.js and Yarn, Docker with `docker compose`.

```bash
make install                                         # dependencies (API and interface)
cp host-panel-api/.env.example host-panel-api/.env   # then set OIDC, FRONT_URL and the secrets
make dev                                             # SFTPGo, then the API and the interface
```

- API: http://localhost:2538
- Interface: http://localhost:5173

| Target | Description |
|---|---|
| `make dev` | SFTPGo, then the API and the interface in parallel (Ctrl-C stops the API and the interface; `make down` stops SFTPGo) |
| `make api` / `make web` | The API only / the interface only |
| `make sftpgo` / `make sftpgo-logs` / `make down` | Start SFTPGo / follow its logs / stop it |
| `make install` | Install the dependencies |
| `make clean` | Stop SFTPGo and delete its local data |

## Test

```bash
cd host-panel-api && make test                    # API; make test-integration for the engines on real images
cd host-panel-interface && yarn typecheck && yarn test   # interface; yarn coverage, yarn test:e2e (Playwright), yarn i18n:check
```

## Documentation

The reference documentation is in [`host-panel-api/docs/`](host-panel-api/docs/README.md).

| To | Read |
|---|---|
| Configure the API | [configuration](host-panel-api/docs/configuration.md) |
| Enable hosting, SFTP or the services | [web hosting](host-panel-api/docs/hosting.md), [SFTP users](host-panel-api/docs/sftp.md), [managed services](host-panel-api/docs/services.md) |
| Understand the security | [security](host-panel-api/docs/security.md), [managed services security](host-panel-api/docs/services-security.md) |
| Use the API | [API reference](host-panel-api/docs/api.md) |
| Customize and translate the interface | [interface README](host-panel-interface/README.md) |

Releases are built by GitHub Actions from `vX.Y.Z-api`, `vX.Y.Z-interface` and `vX.Y.Z` tags, see [continuous integration and releases](host-panel-api/docs/development.md#continuous-integration-and-releases).

To report a vulnerability, see [`SECURITY.md`](SECURITY.md).

## License

Copyright 2026 Loch.sh. Licensed under the [Apache License, Version 2.0](LICENSE); see also [`NOTICE`](NOTICE).
