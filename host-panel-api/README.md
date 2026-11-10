# Loch.sh Panel API

REST and WebSocket API in Go to manage Docker containers, hosted web sites and managed services, with OpenID Connect authentication and per-container access control. It serves the web interface (`host-panel-interface`) and the `loch` CLI (`host-panel-cli`).

## Features

- **Containers**: list, inspect, start, stop, restart and delete containers and compose stacks; shell, live logs, file tails, live stats, file copy and TCP port proxy over WebSocket.
- **Scheduled tasks**: Docker actions, commands in containers and HTTP requests.
- **Users and access**: OIDC login (Microsoft Entra ID, Keycloak or any compliant provider); admin and restricted profiles with per-container permissions; scoped API keys.
- **Web hosting** (optional): PHP, Nginx and Apache sites as plain Docker Compose projects.
- **SFTP users** (optional): accounts for the site owners, through SFTPGo.
- **Managed services** (optional): databases (PostgreSQL, MariaDB, MySQL, MongoDB), caches (Redis, Valkey), message brokers (RabbitMQ, NATS), queues and object storage, with versions discovered on the registries and one-click updates of dedicated instances.
- **Private networks**: internal Docker networks that decide which sites reach which service instances.
- **Audit trail**: every action recorded, consultable by admins only.

## Quick start

```bash
cp .env.example .env    # set the OIDC client, FRONT_URL and the secrets
make dev                # development (go run)
make docker-run         # or build and run the Docker image
```

Requirements: Go 1.27 or later, access to the Docker socket, an OIDC provider. The API listens on port 2538.

## Documentation

| Topic                                                       | Page                                                                                                                                         |
| ----------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------- |
| Environment variables                                       | [docs/configuration.md](docs/configuration.md)                                                                                               |
| Web hosting, SFTP users, managed services, private networks | [docs/hosting.md](docs/hosting.md), [docs/sftp.md](docs/sftp.md), [docs/services.md](docs/services.md), [docs/networks.md](docs/networks.md) |
| Audit trail                                                 | [docs/audit.md](docs/audit.md)                                                                                                               |
| Security                                                    | [docs/security.md](docs/security.md), [docs/services-security.md](docs/services-security.md)                                                 |
| REST routes, WebSocket, API key scopes                      | [docs/api.md](docs/api.md)                                                                                                                   |
| Build, tests, project structure                             | [docs/development.md](docs/development.md)                                                                                                   |

All pages are listed in [docs/README.md](docs/README.md).
