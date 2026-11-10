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

One GitLab pipeline (`.gitlab-ci.yml` at the root of the repository) covers the API and the interface. The project is public and its runner only takes protected refs, so the pipeline runs only on the protected default branch, on protected release tags, and on merge requests between two protected branches of this project; merge requests from forks never run.

| Trigger | Jobs |
|---|---|
| Merge request from a work branch (`feat/*`, `fix/*`, `chore/*`, `docs/*`, `refactor/*`, `perf/*`, `test/*`, `ci/*`) | Checks |
| Push to the default branch | Checks, and `:main` image of each project that changed |
| Tag `vX.Y.Z-api` | API checks, image `api:vX.Y.Z` + `api:latest`, Linux and macOS binaries in the generic package registry, GitLab release |
| Tag `vX.Y.Z-interface` | Interface checks, image `interface:vX.Y.Z` + `interface:latest`, GitLab release |
| Tag `vX.Y.Z` | Both |

The checks:

| Job | What | Report |
|---|---|---|
| `api:test` | `go vet`, `go test` with coverage (`gotestsum`, `gocover-cobertura`) | JUnit, Cobertura, coverage % |
| `api:lint` | `golangci-lint` (default linters), not blocking | Code quality |
| `interface:lint` | ESLint, zero warnings, blocking | Code quality |
| `interface:test` | `yarn typecheck`, `yarn i18n:check`, `yarn coverage` (Vitest, v8) | JUnit, Cobertura, coverage % |
| `interface:e2e` | Playwright on the mocked API | JUnit |
| `sast`, `secret_detection` | GitLab templates (also in merge request pipelines) | Security |

The checks always run on both projects (except on a release tag of the other one), so a merge request compares its code quality and coverage with the default branch: the MR shows the new and fixed code quality findings, the coverage of the changed lines in the diff, and the failed tests. The coverage evolution is in **Analyze > Repository analytics** (one series per job).

Images are built for `linux/amd64` and `linux/arm64` (buildx; the Dockerfiles compile on the build platform, QEMU only runs the runtime stage) and pushed to the project registry (`registry.gitlab.com/loch-sh/panel/api`, `registry.gitlab.com/loch-sh/panel/interface`). The release notes are generated by `git-cliff` (`cliff.toml`, conventional commits) since the previous release of the same scope: a `-api` release lists the commits touching `host-panel-api/` since the previous API or full release, a full release lists every commit since the previous full release.

Project settings this relies on:

- **Protected tags**: `v*`, created by Maintainers only.
- **Protected branches**: the default branch (no force push), and the work branches `feat/*`, `fix/*`, `chore/*`, `docs/*`, `refactor/*`, `perf/*`, `test/*`, `ci/*` (force push allowed). Pushing to any of them is reserved to Maintainers, so only a maintainer's merge request gets a pipeline; a merge request from a branch with another name has none. A force push rule on a wildcard also applies to the branches it matches, hence no `*` rule.
- **Merge request pipelines access protected runners** (`protect_merge_request_pipelines`), and fork merge request pipelines cannot be run in the project (`ci_allow_fork_pipelines_to_run_in_parent_project` off).
- **Runners**: the `loch-sh` group runner (Linux, Docker executor in privileged mode for Docker-in-Docker) with "Run on protected branches only"; instance (shared) runners disabled for the project. Forks live outside the group and cannot use it.
- **Public pipelines** disabled: job logs and artifacts are visible to project members only.
- **Pipeline variables** cannot be overridden (`no_one_allowed`).
- **CI/CD variables**: protected and masked.
