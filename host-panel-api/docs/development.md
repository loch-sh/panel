# Development

## Requirements

- Go 1.27 or later
- Docker with `docker compose` (the API drives the Docker socket)
- An OIDC provider (see [Configuration](configuration.md#authentication-oidc))

## Commands

```bash
cp .env.example .env    # then edit it
make dev                # run in development (go run)
make test               # unit tests
make test-integration   # integration tests on real images (Docker required, a few minutes)
make build              # compile
make docker             # build the Docker image
```

| Target | Description |
|---|---|
| `make help` | List the targets (default) |
| `make dev` | Run in development (`go run`, no reload: restart it after a change) |
| `make build` / `make run` | Compile / compile and run |
| `make test` | Unit tests (`go test ./...`) |
| `make test-integration` | Each service engine on its real images (build tag `integration`) |
| `make vet` / `make fmt` / `make tidy` | Static analysis / formatting / dependencies |
| `make docker` / `make docker-run` | Build the image / build and run it |
| `make sftp-key` | Create an SFTPGo admin API key, needs `curl` and `jq` (see [SFTP users](sftp.md)) |
| `make clean` | Remove generated files |

## Project structure

```
cmd/server/            entry point: configuration, wiring, routes
internal/
├── audit/             audit log (writer and reader)
├── auth/              OIDC providers, JWKS, signed cookies
├── composekit/        shared helpers to write and run compose projects safely
├── config/            environment loading and validation
├── env/               environment reader and linter (every problem at once, unknown and removed variables)
├── cron/              scheduled tasks (scheduler, executor, store)
├── docker/            Docker client, container cache, exec, networks
├── handler/           HTTP and WebSocket handlers
├── hosting/           web hosting plugin
├── middleware/        authentication, CSRF, CORS, rate limiting, audit, logging
├── model/             users, API keys, stats
├── networks/          private networks: registry, verification, members
├── registry/          image references (mirror.gcr.io, ghcr.io)
├── releases/          version discovery: registry tags, mirror digests, endoflife.date
├── services/          managed services core (instances, resources, exposure, TLS)
│   ├── postgres/  mysqlfamily/  mongodb/        database engines
│   ├── redisfamily/  nats/  rabbitmq/           cache and messaging engines
│   ├── elasticmq/  mosquitto/                   queue engines
│   └── s3/                                      object storage engine (Loch S3)
├── sftpgo/            SFTPGo admin client
├── store/             JSON persistence (atomic writes)
└── ws/                WebSocket hub, logs, shell, stats, port proxy
```

## Conventions

- Docker images are pulled from `mirror.gcr.io`, Google's mirror of Docker Hub, never from Docker Hub directly: `mirror.gcr.io/library/<name>` for official images, `mirror.gcr.io/<org>/<name>` for the others. In Go, build references with `registry.Hub("postgres:17")`. An image missing from the mirror stays on Docker Hub and is listed explicitly (see `notMirrored` in `internal/hosting/catalog.go`). Our own images (Loch S3) come from `ghcr.io` through `registry.GHCR(...)`, the only other exception.

- API messages, logs and code comments are in English. The web interface translates the error codes.
- Responses go through `writeJSON` / `writeError` with a stable error code.
- Stores write atomically (temporary file, then rename) behind a read-write lock.
- Anything a user can trigger is audited, without secrets.
- Every environment variable is read through an `env.Reader` (never `os.Getenv`), so that the startup lint sees it; a removed variable is declared with `Reader.Removed` and a new prefix added to `lintedPrefixes` (`internal/config`). Never put a value in a lint message.

## Adding a service engine

An engine implements `services.Engine`:

- where its versions are published: `Releases()` (`Source`, the repository whose tags are listed on the origin registry; `Tag`, a strict regular expression whose group 1 is the version and optional group 2 a patch number, for engines that only publish exact tags such as Mosquitto, ElasticMQ and Loch S3; `LineTag`, the format of the floating tag of a version, e.g. `%s-alpine`, empty for exact-tag engines; `EOL`, the endoflife.date product, empty when it is not listed; `Min`, the oldest version the code supports), and `VersionOf(version, tag)`, which builds the image, data mount and platform of any version from a tag. The versions are then discovered by the panel (see [Managed services](services.md#versions-and-updates)); `Catalog()` stays as the built-in fallback used until a discovery succeeds. An engine only declares a minimum: newer majors are offered untested, so add the checks a new major could break (`OptionValidator`);
- the engine-specific part of the compose service, the data owner and the connection information;
- either a `Provisioner`, for shared instances, or a `DedicatedEngine`, for dedicated ones (options and password state files);
- optionally `TLSCapable`.

A dedicated engine is also updatable (image, version): the update re-renders everything from the engine code, so `StateFiles` and `Service` must depend only on the version, the options and the password digest. Register it in `serviceEngines()` (`cmd/server/main.go`), then add its menu entry and translations in the web interface. Every value rendered into a configuration must come from a fixed list or a validated identifier.

Read [Managed services security](services-security.md) first, and add an integration test on its real images.

## Continuous integration and releases

GitHub Actions (`.github/workflows/`) on GitHub-hosted runners (ephemeral virtual machines: no self-hosted runner, nothing shared between jobs). Actions are pinned to commit SHAs with their version in a comment; Dependabot (`.github/dependabot.yml`) proposes the updates of the actions, the Go and npm dependencies and the Docker base images.

| Workflow | Trigger | Jobs |
|---|---|---|
| `ci.yml` | Push to `main`, pull request | Checks; on `main`, the `:main` images of both projects |
| `release.yml` | Tag `vX.Y.Z-api` | API checks, image `api:vX.Y.Z` + `api:latest`, Linux and macOS binaries, GitHub release |
| `release.yml` | Tag `vX.Y.Z-interface` | Interface checks, image `interface:vX.Y.Z` + `interface:latest`, GitHub release |
| `release.yml` | Tag `vX.Y.Z` | Both |
| `codeql.yml` | Push to `main`, pull request, weekly | CodeQL (Go, TypeScript): Security tab |

The checks (`checks.yml`, called by both):

| Job | What |
|---|---|
| API tests | `gofmt`, `go vet`, `go test -race` with coverage, `govulncheck` |
| API lint | `golangci-lint` (`.golangci.yml`: standard linters, errors conventionally ignored such as `Close` excluded), blocking |
| Interface lint | ESLint, zero warnings, blocking |
| Interface test | `yarn typecheck`, `yarn i18n:check`, `yarn coverage` (Vitest, v8) |
| Interface e2e | Playwright on the mocked API (results kept as an artifact on failure) |

Pull requests from forks run the checks with a read-only token and no secret; only pushes to `main` and release tags publish images and releases. A release tag must point to a commit of `main`, and a version is published once: an image `vX.Y.Z` that already exists is never overwritten (a full release `vX.Y.Z` after `vX.Y.Z-api` fails: take the next version). Nothing is pushed when the binaries fail; release builds use no cache; `-api` and `-interface` releases are not marked as the repository's latest release.

Images are built for `linux/amd64` and `linux/arm64` (buildx; the Dockerfiles compile on the build platform, QEMU only runs the runtime stage) and pushed to GitHub Container Registry: `ghcr.io/loch-sh/panel/api` and `ghcr.io/loch-sh/panel/interface`, with an SBOM and SLSA build provenance attestations (`gh attestation verify oci://ghcr.io/loch-sh/panel/api:vX.Y.Z -R loch-sh/panel`). The API binaries (`host-panel-api-linux-amd64`, `-linux-arm64`, `-darwin-arm64`) are attached to the release with their `.sha256` and a provenance attestation. The release notes are generated by `git-cliff` (`cliff.toml`, conventional commits) since the previous release of the same scope: a `-api` release lists the commits touching `host-panel-api/` since the previous API or full release, a full release lists every commit since the previous full release.

Repository settings this relies on (same model as the other loch-sh repositories):

- **Ruleset `release tags`** (`refs/tags/v*`): creating, moving or deleting a release tag is reserved to repository administrators, and the tagged commit must be signed (a release tag publishes images and binaries).
- **Ruleset `main`**: no deletion, no force push, signed commits only.
- **Actions**: workflow token read-only by default (each job asks for what it needs), actions must be pinned to a full commit SHA, approval required before running the workflows of a first-time contributor's pull request.
- **Security**: secret scanning with push protection, Dependabot alerts and security updates, private vulnerability reporting (see `SECURITY.md`).
