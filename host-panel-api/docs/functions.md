# Functions (work in progress)

> **Work in progress.** The API (manager, HTTP routes, invocation, scheduled invocations, secrets, credentials, networks, audit), the interface ("Serverless > Functions") and the CLI (`loch functions`; `loch functions dev` for local development is coming) exist. The panel build trusts the loch function runtime v1.0.1 and installs it by itself (see [Runtime binary](#runtime-binary)): while it is not installed, operations needing it answer `FUNCTION_RUNTIME_UNAVAILABLE` (503). The plan is in [the decision log, §20](design/services-decisions.md).

Functions run handlers written for AWS Lambda™ (Node.js and Python, or a compiled `bootstrap` on the custom runtime: Go, Rust, …) on the host, without Kubernetes and without a vendor SDK: the same code also runs on AWS Lambda without changes. Loch.sh is not affiliated with or endorsed by Amazon (see [Trademarks](#trademarks)). The security model is in [Functions security](functions-security.md).

## How a function runs

- **One compose project per function**, `fn-<name>` in `HOSTING_STACKS_DIRECTORY`, startable with `docker compose up -d` without the panel (the header of `compose.yaml` lists the `docker network create` commands of its networks).
- **The image is the AWS Lambda base image published by AWS**, unchanged, pulled through `mirror.gcr.io` and pinned by digest: `mirror.gcr.io/amazon/aws-lambda-nodejs:<tag>@sha256:…` or `…/aws-lambda-python:…`. The tags carry the architecture (`22-arm64`, `3.13-x86_64`, or dated builds such as `22.2026.11.05.18-arm64`).
- **The entrypoint is loch's own implementation of the AWS Lambda Runtime API** (`loch-lambda-runtime`), bind-mounted read-only at `/loch/lambda-runtime`; the AWS entrypoint and the handler follow `--`:

  ```yaml
  entrypoint: ["/loch/lambda-runtime"]
  command: ["--", "/lambda-entrypoint.sh", "index.handler"]
  ```

- **The code** is a zip (handler and dependencies, never built on the server), validated and extracted by the panel into a read-only deployment directory mounted at `/var/task`.
- **Variables** are in `compose.yaml` (`$` escaped); **secrets** are in an `env_file` (`format: raw`, 0400) in the private directory, never in `compose.yaml`.
- The panel sets `AWS_LAMBDA_FUNCTION_NAME`, `AWS_LAMBDA_FUNCTION_MEMORY_SIZE` (equal to the memory limit), `AWS_LAMBDA_FUNCTION_TIMEOUT`, `AWS_REGION` (`us-east-1`), `AWS_EC2_METADATA_DISABLED=true`, `PYTHONDONTWRITEBYTECODE=1` and `LOCH_RUNTIME_SHUTDOWN_GRACE`; a second `env_file` (`runtime.env`, 0400) holds what the runtime checks the invoke credentials with (see [Invoke credentials](#invoke-credentials)).
- **Networks**: a function never joins the `services` network. It gets its own private network `fn-<name>` by default; outbound Internet access is an explicit option (a compose-owned network `fn-<name>_fn_egress`, not internal).
- No published port. The runtime answers on port 8080 to the members of the function's networks, with authentication.

### Names

A function name is 1 to 21 lowercase letters, digits or dashes, starting with a letter and not ending with a dash (so that its default network `fn-<name>` fits the 24 characters of a network name); `config`, `new`, `default`, `all`, `latest`, `function`, `functions`, `deployments`, `invoke`, `panel`, `loch`, `system` and `api` are reserved.

### Handler

`<file>.<function>` for Node.js (`index.handler`, `src/app.handler`), `<module>.<function>` for Python (`lambda_function.lambda_handler`, `app.main.handler`). Path components use letters, digits, `_` and `-`; no `.` or `..` component.

On the custom runtime (`provided`) the handler is optional and defaults to `bootstrap` (an empty handler in `PATCH` resets it): as on AWS, it is only exported to the bootstrap as `_HANDLER`, which may read it. It is letters, digits, `_`, `-` and `.`, optionally slash-separated, no component starting with `.` or `-`.

### Custom runtime (Go, Rust, any compiled language)

Runtime `provided`, version `al2023`: the OS-only image `amazon/aws-lambda-provided` (`al2023-arm64` / `al2023-x86_64`, through `mirror.gcr.io`), started exactly like the other runtimes (`-- /lambda-entrypoint.sh <handler>`). The image script exports its argument as `_HANDLER` and runs `/var/task/bootstrap` (then `/opt/bootstrap`, never another name): the zip must hold an executable named `bootstrap` **at its root**. It is checked at deploy time, on the extracted tree:

- `bootstrap` exists at the root and is a regular file: `FUNCTION_BOOTSTRAP_MISSING` (also when it is only found in a folder: zip the file, not its folder), `FUNCTION_BOOTSTRAP_INVALID` for a symlink or a directory;
- it is a 64-bit little-endian Linux ELF executable (static, or linked against glibc) for the function's architecture (`FUNCTION_BOOTSTRAP_ARCH` with the build command otherwise, e.g. "bootstrap is built for amd64, the function runs on arm64: GOOS=linux GOARCH=arm64 …"), or a `#!` script (accepted as is); anything else (Mach-O, PE, object file, other OS, more than 250 MB) is `FUNCTION_BOOTSTRAP_INVALID`;
- a dynamically linked binary using the glibc loader of al2023 is deployed with the warning `FUNCTION_BOOTSTRAP_DYNAMIC` (in the deployment's `warnings`: it runs only if built against a glibc no newer than al2023's); another loader (musl…) is refused, `FUNCTION_BOOTSTRAP_INVALID`, since al2023 has none;
- the exec bit of `bootstrap` is not trusted from the zip (Windows tools drop the Unix modes): `bootstrap` is forced executable (`0555`); other files keep the exec bit they have in the zip (native addons, helper binaries).

A bootstrap that passes these checks but does not start (crash, missing library) never becomes healthy: `compose up --wait` fails, the deployment answers `FUNCTION_START_FAILED` and the previous one is kept.

Go example, with [aws-lambda-go](https://github.com/aws/aws-lambda-go) (`main.go`):

```go
package main

import (
	"context"
	"errors"

	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-lambda-go/lambdacontext"
)

type event struct {
	Name string `json:"name"`
}

func handle(ctx context.Context, e event) (map[string]string, error) {
	lc, ok := lambdacontext.FromContext(ctx)
	if !ok {
		return nil, errors.New("no Lambda context")
	}
	return map[string]string{"hello": e.Name, "requestId": lc.AwsRequestID}, nil
}

func main() { lambda.Start(handle) }
```

Build a static binary for the architecture of the Docker host (`arch` in `GET /functions/config`), zip it at the root, deploy:

```bash
go mod init example.com/hello && go mod tidy
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -tags lambda.norpc -o bootstrap .
zip function.zip bootstrap
loch functions create hello-go --runtime provided --version al2023
loch functions deploy hello-go function.zip
```

`CGO_ENABLED=0` gives a static binary (no glibc dependency); `-tags lambda.norpc` leaves out the legacy RPC mode of aws-lambda-go, which the provided runtimes never use. Rust (`cargo lambda build --release --arm64`, or a `musl` target statically linked) works the same way: the binary must be named `bootstrap`.

### Variables and secrets

Names are letters, digits and underscores, not starting with a digit; values are one line of UTF-8 (no NUL, CR or LF), at most 4096 bytes; at most 100 variables and secrets together, 64 KiB of variables. Reserved (set by the panel, the runtime or the AWS bootstrap, or able to change how code is loaded), case-insensitive: the prefixes `AWS_LAMBDA_`, `LAMBDA_`, `LD_`, `LOCH_RUNTIME_`, `AWS_CONTAINER_`, `AWS_XRAY_`, `_`, and `AWS_REGION`, `AWS_DEFAULT_REGION`, `AWS_EXECUTION_ENV`, `AWS_EC2_METADATA_DISABLED`, `PATH`, `HOME`, `HOSTNAME`, `PYTHONDONTWRITEBYTECODE`.

`AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY`, `AWS_SESSION_TOKEN` and `AWS_ENDPOINT_URL_*` stay allowed: they are how a function reaches Loch S3 with the AWS SDKs.

### Resources

| Setting | Range | Default |
|---|---|---|
| Memory | 128 MB to `FUNCTIONS_MAX_MEMORY` | 512 MB |
| CPU | 0.1 to 16, in steps of 0.01 | 1 |
| Timeout | 1 to 900 s | 30 s |
| `/tmp` size | 16 MB to 10 GB | `FUNCTIONS_TMP_SIZE` |

The `/tmp` tmpfs counts against the memory limit (a function filling `/tmp` is killed for lack of memory). As on Lambda, files in `/tmp` can be executed (binaries downloaded at run time).

## Lifecycle

- **Create** (`POST /functions`): validates the configuration, checks the quotas (`FUNCTIONS_MAX`, and the sum of the memory and CPU limits of every function against `FUNCTIONS_MAX_TOTAL_MEMORY` / `FUNCTIONS_MAX_TOTAL_CPUS`), refuses a name already used by a directory, a container, a compose project or a network (`FUNCTION_NAME_CONFLICT`: the panel never adopts anything by name), pins the image (below), creates the private network `fn-<name>`, the private directory, an empty secrets file and the invoke credentials. Nothing runs until code is deployed (`not_deployed`).
- **Deploy** (`POST /functions/{name}/code`): the zip becomes a new deployment, activated at once; the previous one is kept, and at most `FUNCTIONS_DEPLOYMENTS_KEPT` deployments are kept (the active one and the one active until now always; the oldest others are removed). A deployment that does not start is removed and the previous one started again.
- **Activate** a kept deployment (rollback): its tree is verified first.
- **Configuration** (`PATCH`): memory, CPU, timeout, `/tmp` size, handler, variables, MCP opt-in; a running function is recreated with them. A **stopped** function stays stopped: changes are written, applied at the next start.
- **Start / stop / restart**: stopping goes through the Docker API, never compose (it works on a modified function too).
- **Scopes**: `functions:write` covers the resources, start / stop / restart, creation and deletion; variables, handler, networks and egress need `functions:deploy` too (they decide where the code can send its secrets), and so does the MCP opt-in.
- **MCP opt-in** (`mcpInvocable`, off by default; at creation or by `PATCH`, alone it recreates nothing): lets the loch MCP tool invoke the function. The MCP client checks it; the API does not and cannot (see [Functions security](functions-security.md#mcp-opt-in-mcpinvocable)).
- **Delete**: containers (removed through the Docker API, by project, working directory or name), the egress network, the compose project, every deployment and upload leftover, secrets, credentials and reference, then the network `fn-<name>` (kept, `networkKept`, while other members are attached). **Purge** (`?purge=true`) does the same for a function whose compose project was edited outside the panel, or whose reference is unreadable, without running any of its files.

Every compose run goes through one choke point: the runtime binary is re-hashed and the deployment tree re-verified, the pinned image checked (pulled again by digest if it was removed), the compose project refused if it is not byte for byte what the panel renders (`FUNCTION_MODIFIED`), the files written, `docker compose -f - up -d --pull never --wait` with the rendered bytes on stdin (the file on disk, writable by CI, is never what compose reads), then the container verified against the reference and the project required to hold that one container only (`FUNCTION_VERIFICATION_FAILED`). On any failure every file is restored and the previous configuration started again (or the function stopped when networks were cut). Operations on one function are serialized (`FUNCTION_OPERATION_IN_PROGRESS` after 4 minutes of waiting); different functions run in parallel.

At startup the panel removes upload leftovers and logs the functions modified outside the panel.

### States

`not_deployed`, `running`, `stopped`, `missing` (deployed, not stopped, no container), `modified` (never run: purge it, or restore the rendered file), `unverified` (a container runs but does not match the reference; `problem` says why), `invalid` (unreadable reference: purge only). `GET /functions` reads the states from the container list; `GET /functions/{name}` verifies the container fully.

### Image

Created functions use the official image of the architecture-suffixed line tag (`22-arm64`, `3.13-x86_64`): its digest is read on `mirror.gcr.io`, the image pulled by that digest and its ID recorded in the reference. The mirror answers `not found` or 429 on the first requests of a tag: both steps are retried (5 attempts, backoff 2 to 16 s). Offered versions: Node.js 26, 24, 22; Python 3.14, 3.13, 3.12; custom runtime al2023 (`al2023-arm64`, `al2023-x86_64`) (built in until the runtime updates of a later phase).

## Invoke credentials

The runtime refuses network callers without a credential. At creation and at each rotation (`POST /functions/{name}/credentials/rotate`) the panel generates them and returns them **once**; it keeps only what the runtime needs:

- **Bearer token** (`Authorization: Bearer <token>`, 32 random bytes): only its SHA-256 is passed to the container (`LOCH_RUNTIME_TOKEN_SHA256`). Every variable of a container is readable by the function code, so the token itself never enters it; neither the panel nor the container can show it again.
- **SigV4 key pair** (AWS SDK callers with `endpoint-url`), **off by default** (`sigv4` at creation, or `{"sigv4": true}` on a rotation): a random 20-character access key id and a 40-character secret. The runtime needs the secret itself (`LOCH_RUNTIME_ACCESS_KEY=<id>:<secret>`), so **the function code can read it**; it only lets the function invoke itself.
- The endpoint is `http://fn-<name>:8080/2015-03-31/functions/<name>/invocations`, region `us-east-1`, for the members of the function's networks. A rotation recreates the container.
- Loopback callers need no credential: the panel's own invocations (`docker exec`) and the healthcheck.

## Invocation

`POST /functions/{name}/invoke` with the event as body (one JSON value of any type, at most 6 MiB, received within 30 s) runs `docker exec <verified container id> /loch/lambda-runtime invoke --watch-stdin --log-tail` as uid 10001: the panel never joins a function network. The event is written on stdin, followed by a newline, and stdin stays open: when the panel closes the exec connection (envelope read, deadline, or client gone), Docker closes the process's stdin and the runtime's invoke exits within about a second (Docker never closes an exec's stdout). The function and its state are checked, and a slot taken, before the body is read; the container is verified before each invocation and the exec targets the verified container ID. At most 2 invocations per function are in flight from the panel (a third waits 5 s, then `FUNCTION_INVOKE_BUSY`); a slot is released when the exec ends, one second later when it was cut short. The runtime queues the others itself. The exec deadline is the function timeout + 30 s (the runtime's queue wait) + 5 s (its margin) + 10 s; the invoke subcommand always exits at its own deadline first. The result is a fixed JSON envelope (status, function error, request id, duration, payload as text or base64, last 4 KiB of logs): untrusted data, rendered as text only. Invocations are audited aggregated per function and per minute (`function invoked`: count, function errors, failures, users), never with their content.

### Scheduled invocations (cron)

A scheduled task of type `function_invoke` (`POST /cron`, see [the API](api.md#function_invoke-tasks)) invokes a function with a fixed event (JSON text, at most 64 KiB, `{}` by default) through `Manager.Invoke`, exactly as above: `docker exec`, never HTTP nor a network bridge, the panel's 2 slots per function shared with the other callers.

- **Access**: admins only; an API key needs `functions:invoke` on top of `cron` to create, change or run such a task. Refused while the module is disabled (`FUNCTIONS_DISABLED`), except to keep a task disabled, rename or reschedule it; while the module is disabled the tasks are not scheduled at all.
- **Bounds**: at most 5 tasks per function (`FUNCTION_CRON_LIMIT_REACHED`), a minimum period of 1 minute, and no overlap: a run is skipped (`lastSkipped`) while the previous invocation of the same task still runs.
- **Result**: `lastOutput` keeps the status code, the function error type, the duration and the payload size, never the payload nor the log tail. A function error or a status ≥ 400 fails the run.
- **Audit**: the invocations are counted in the aggregated `function invoked` entry, with `cron:<task id>` as the user.
- **Deletion**: deleting (or purging) a function disables its tasks, which are kept with `disabledReason: "FUNCTION_DELETED"`; they can be pointed at another function or deleted. At startup, an enabled task whose function no longer exists is disabled the same way. Disabling was chosen over refusing the deletion: a function must stay deletable (a compromised one in particular) whatever schedules it.

## Networks and egress

A function joins its own network `fn-<name>` and the private networks it is attached to (`PUT /functions/{name}/networks`, or the networks routes with the member kind `function`); never `services`. A removed network is cut before the container is recreated; if the recreation fails the function is stopped, never reconnected. Outbound Internet access (`PUT /functions/{name}/egress`) is off by default: when on, it reaches the host gateway, the LAN and metadata endpoints unless `DOCKER-USER` rules say otherwise (audited as a warning).

## Code upload

The zip is streamed to a temporary file, never held in memory, then validated entry by entry before anything is extracted. Refused: names that are not clean relative UTF-8 paths, encrypted entries, compression methods other than stored and deflate, devices, pipes, sockets, symlinks leaving the code or not pointing to a file of the zip, and archives over the limits below. The extracted tree is made read-only and hashed; the hash is checked before every start. Details in [Functions security](functions-security.md#code-upload).

| Limit | Value |
|---|---|
| Zip size | `FUNCTIONS_MAX_CODE_SIZE` (50 MB) |
| Extracted size, total and per file | 250 MB |
| Entries | 50,000 |
| Directories, explicit or implied by a path | 20,000 |
| Compression ratio | 100:1 per file and in total (sizes under 1 MB are never refused for their ratio) |
| Duration | 2 minutes |
| Concurrent uploads | bounded; beyond, `FUNCTION_UPLOADS_BUSY` |
| Pace | no progress for 15 s, or under 32 KiB/s on average after 30 s: cut (`FUNCTION_CODE_TIMEOUT`, 408) |
| Free space and inodes | the worst case of every running upload is reserved; beyond, `FUNCTION_NO_SPACE` |

The zip itself is not kept: only its sha256 is recorded with the deployment. Temporary zips and staging directories left by an interrupted upload are removed at startup (`Paths.SweepLeftovers`).
## Configuration

| Variable | Default | Meaning |
|---|---|---|
| `FUNCTIONS_ENABLED` | `false` | enables the module |
| `FUNCTIONS_DATA_DIRECTORY` | `/data/functions` | code, runtime binaries, secrets and references; same path on the host and in the API container; letters, digits and `_ . / -` only (it is written in the compose files); outside `HOSTING_STACKS_DIRECTORY`, `HOSTING_SITES_DIRECTORY`, `SFTP_DIRECTORY`, `SERVICES_DATA_DIRECTORY` and `API_LOG_DIR`, symlinks resolved (checked at startup); a subdirectory of `DATA_DIR` is fine (for example `DATA_DIR/functions`), `DATA_DIR` itself or a parent of it is refused |
| `FUNCTIONS_MAX` | `50` | number of functions |
| `FUNCTIONS_MAX_MEMORY` | `3008` | memory of one function, MB (or `3G`) |
| `FUNCTIONS_MAX_CODE_SIZE` | `50` | size of an uploaded zip, MB, 1 to 250 |
| `FUNCTIONS_DEPLOYMENTS_KEPT` | `3` | deployments kept for a rollback, the active one included |
| `FUNCTIONS_TMP_SIZE` | `512` | default `/tmp` size of a function, MB |
| `FUNCTIONS_MAX_TOTAL_MEMORY` | `0` | sum of the memory limits of every function, MB (or `64G`); `0` = the memory of the Docker host |
| `FUNCTIONS_MAX_TOTAL_CPUS` | `0` | sum of the CPU limits of every function; `0` = 4 × the CPUs of the Docker host |
| `FUNCTIONS_RUNTIME_DOWNLOAD` | `true` | find the newest runtime release on GitHub (at startup and every hour) and download its binary (see [Runtime binary](#runtime-binary)); `false` for offline installations |
| `FUNCTIONS_RUNTIME_SOURCE_DIR` | (empty) | absolute path of a directory holding one directory per release (`vX.Y.Z/`, with its `SHA256SUMS` and `loch-lambda-runtime-linux-<arch>` files): read instead of downloading (takes precedence over `FUNCTIONS_RUNTIME_DOWNLOAD`); it, its release directories and their files must be owned by the panel's user or root and not writable by group or others (checked at startup) |

Sizes are in MB and accept a binary unit (`512M`, `512MiB`, `1G`).

## Runtime binary

Every function runs the loch function runtime ([loch-sh/lambda-runtime](https://github.com/loch-sh/lambda-runtime)), a static Go binary, published as GitHub [releases](https://github.com/loch-sh/lambda-runtime/releases) (`https://github.com/loch-sh/lambda-runtime/releases/download/<version>/<file>`).

**Which release.** At startup, then every hour, the panel reads the list of releases (`https://api.github.com/repos/loch-sh/lambda-runtime/releases`, anonymous: 60 requests an hour per address, a check uses one) and takes the newest stable version (`vX.Y.Z`, drafts and prereleases ignored), **v1.0.1 or later** (the release the panel builds trusted before; functions created with it keep running). Until the record exists, it starts with the v1.0.1 sha256 those builds embedded (equal to the published ones), so an upgraded panel keeps using the binaries already installed, offline too. It reads that release's `SHA256SUMS`, installs the binary of the Docker host's architecture when it has the published sha256, and only then records that sha256 in `FUNCTIONS_DATA_DIRECTORY/.runtime/releases.json` (0600, owned by the panel; the API refuses to enable the module when the file or its directory was changed by someone else)). New functions get the newest installed release; a function keeps the release (and the sha256) it was created with.

**Trust.** GitHub is the trust root: its TLS, the `loch-sh/lambda-runtime` repository and the people allowed to publish releases there. No signature is verified by the panel. What it adds:
- a recorded sha256 never changes: a release published again with other bytes is refused, never stored, and audited (`function runtime refused`), the binary already installed stays;
- a binary is stored only when its sha256 is the published one (and the recorded one, if any) and it is a Linux ELF executable of the architecture; a `SHA256SUMS` that does not match its binary records nothing, so a corrected release installs afterwards; it is re-hashed before every start against the sha256 the function pinned;
- a binary missing from the store (a data directory restored without `.runtime`), or present but not recorded, is fetched again from its own release (v1.0.1 or later only) and kept only when the release publishes the sha256 the function pinned; deleting `releases.json` by hand resets the record to the v1.0.1 entries (the other sha256 are taken from GitHub again).
- GitHub's anonymous rate limit (60 API requests an hour per address): a check reads the releases list once (the release files do not count); on-demand attempts reuse the version found by the last check for the hour; when GitHub answers with its rate limit, no attempt is made before the time it announces (at most an hour).

To check a release by hand, its `SHA256SUMS` is signed with cosign keyless by the repository's release workflow, and each binary has a GitHub build provenance attestation:

```sh
V=vX.Y.Z
B=https://github.com/loch-sh/lambda-runtime/releases/download/$V
for f in loch-lambda-runtime-linux-amd64 loch-lambda-runtime-linux-arm64 SHA256SUMS SHA256SUMS.cosign.bundle; do
  curl -fsSLO "$B/$f"
done
cosign verify-blob --bundle SHA256SUMS.cosign.bundle \
  --certificate-identity "https://github.com/loch-sh/lambda-runtime/.github/workflows/ci.yml@refs/tags/$V" \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com SHA256SUMS
shasum -a 256 -c SHA256SUMS --ignore-missing
```

**Installation.** When the newest release is not installed, the panel installs it in the background (retries with backoff: 15 s, 30 s, 1 min, 2 min, 5 min, 10 min, 30 min, then on demand or at the next hourly check). The module answers meanwhile: creating a function (or starting one whose binary is missing) answers `FUNCTION_RUNTIME_UNAVAILABLE` (503) and starts a new attempt (at most one per minute), the reason is in the API logs. `GET /functions/config` shows `runtime: {state, version}` (and `runtimeReady`): `idle` (nothing attempted yet), `installing`, `installed` (with `version`), `unavailable` (failed, no source, or no binary for the host architecture: see the logs) or `untrusted` (a binary or a release was refused: no on-demand retry for 30 minutes).

- **Download** (default): HTTPS only, with a client of its own (5 min timeout; `HTTPS_PROXY` is honored). Outbound hosts: `api.github.com` (releases list), `github.com` (release files), then the redirect to `release-assets.githubusercontent.com` or `objects.githubusercontent.com` (HTTPS only, any other host is refused). The releases list is capped at 1 MiB, `SHA256SUMS` at 64 KiB, a binary at 64 MiB; the binary is streamed into the store, hashed and checked; a mismatch is refused, never stored, and audited (`function runtime refused`).
- **Offline installations**: set `FUNCTIONS_RUNTIME_DOWNLOAD=false` and either install nothing (no function can start), or copy releases into a directory and point `FUNCTIONS_RUNTIME_SOURCE_DIR` at it, one directory per release:

  ```sh
  # on a machine with Internet access, then copy the directory to the host
  V=v1.0.1
  mkdir -p loch-runtime/$V && cd loch-runtime/$V
  for f in loch-lambda-runtime-linux-amd64 loch-lambda-runtime-linux-arm64 SHA256SUMS; do
    curl -fsSLO "https://github.com/loch-sh/lambda-runtime/releases/download/$V/$f"
  done
  shasum -a 256 -c SHA256SUMS --ignore-missing
  # mount loch-runtime read-only in the API container, e.g. /opt/loch-runtime
  FUNCTIONS_RUNTIME_SOURCE_DIR=/opt/loch-runtime
  ```

  The panel takes the newest `vX.Y.Z` directory (v1.0.1 or later), reads its `SHA256SUMS` and checks the binary the same way (sha256, ELF architecture; the recorded sha256 never changes); the directory is read at startup, every hour and on demand, never written. It, its release directories, their `SHA256SUMS` and runtime files must be owned by the panel's user or root and writable by nobody else (no group or world write bit), and the files must be regular files, not symlinks: otherwise the API refuses to start (environment lint). Each read opens the file without following a symlink and without blocking (a FIFO or a device is refused).

- An installed binary is never replaced: a stored binary that fails verification is reported (`FUNCTION_ARTIFACT_MODIFIED` / `FUNCTION_RUNTIME_UNTRUSTED`, audited `function runtime refused` with the reason `stored binary modified`) and must be removed by hand before the panel installs it again.

## Files

```
FUNCTIONS_DATA_DIRECTORY/
  .loch/fn-<name>/              0700  private directory
    function.json               0600  reference: what the panel trusts
    secrets.env                 0400  secrets (env_file, format: raw)
    runtime.env                 0400  token SHA-256 (+ SigV4 key pair) for the runtime (env_file, raw)
  .runtime/                     0700
    releases.json               0600  sha256 of the installed releases (from GitHub, never changed)
    <version>/<arch>/
      loch-lambda-runtime       0555  runtime binary, written once
  fn-<name>/
    deployments/<id>/           0555  extracted code, mounted read-only at /var/task
    uploads/                    0700  zips being validated (temporary)
HOSTING_STACKS_DIRECTORY/
  fn-<name>/compose.yaml              rendered by the panel, compared before every use
```

Deleting the compose directory does not delete the code or the secrets. Every directory above must be owned by the panel and writable by nobody else: the panel refuses to use it otherwise.

## Tests

Besides the unit tests, `internal/functions/e2e_integration_test.go` (build tag `integration`) runs the module end to end against a real Docker daemon, a `loch-lambda-runtime` build and the AWS Lambda base images: creation, deployment, invocation (Node.js 22, Python 3.13, a Go bootstrap on provided.al2023 built from `testdata/gofunc` — wrong architecture refused, exec bit set by the panel, a bootstrap that cannot start rolled back —, scalar and 6 MiB events, function error, timeout, out of memory, concurrency, caller gone), rollback and tampered deployments, secrets and masking, bearer and SigV4 callers from other containers, rotation, edited compose files and containers recreated by hand, networks and egress, image pinning (also under the containerd image store), hardening, a panel restart, deletion and purge. With `LOCH_E2E_RUNTIME_DIR` (a `FUNCTIONS_RUNTIME_SOURCE_DIR`: one `vX.Y.Z/` directory per release with its `SHA256SUMS`), the newest release is found and installed the way the panel does; `LOCH_E2E_RUNTIME_BINARY` (a single build) gets a test record holding its sha256:

```sh
LOCH_E2E_RUNTIME_DIR=<dir with vX.Y.Z/{SHA256SUMS,loch-lambda-runtime-linux-*}> \
  go test -tags integration -run E2E -count=1 -timeout 40m -v ./internal/functions/
```

Everything it creates is named `fn-e2e*`, `loch-net-fn-e2e*` or `e2efn-*` and removed at the end.

`internal/functions/runtimefetch_integration_test.go` finds the newest release on GitHub and installs it for both architectures, as the panel does (Internet access required):

```sh
LOCH_E2E_DOWNLOAD=1 go test -tags integration -run RealDownload -count=1 -v ./internal/functions/
```

## Local development

`loch functions dev` (CLI, no panel nor API key) runs a function on the developer's machine with Docker and the loch function runtime, in a container that mirrors the one `Render` produces: same base image (line tag, not pinned by digest), uid 10001, entrypoint and command, read-only root, `cap_drop: ALL`, `no-new-privileges`, tmpfs, shm, `ipc: private`, ulimits, PID/memory/CPU limits, health check, stop grace period, logging, `traefik.enable=false` and the panel's variables; the reserved variable names are refused the same way. The local differences, listed in the header of the compose file it renders: the code directory and the runtime binary bind-mounted read-only, an explicit platform, the invoke port published on `127.0.0.1` only with locally generated credentials (token SHA-256 and SigV4 key pair in a cache env file; `--no-auth` sets `LOCH_RUNTIME_AUTH=off`), every variable in one env file, its own network (outbound access on by default, `--egress=false` for an internal one), `restart: "no"`, no `x-loch` manifest, and the label `sh.loch.dev-function` on a `loch-dev-<name>` project instead of `sh.loch.function` / `sh.loch.managed` on `fn-<name>`. `dev invoke` runs the same `docker exec … invoke --watch-stdin --log-tail` as the panel. Usage: the CLI README, "Local development". It also offers `--watch` (polling restart on code changes, Enter to restart), `--from FUNCTION` (the settings and the variable and secret names of a deployed function, never their values; `--exact-image` runs the deployed digest-pinned image) and `loch functions dev event` (sample S3, SQS, API Gateway v2 and schedule events).

**Runtime binary of `loch functions dev`**: unless `--runtime-binary` or `LOCH_RUNTIME_BINARY` names a local build (used as given, warned when its sha256 is not trusted), the CLI downloads the newest release (`--runtime-version vX.Y.Z` for another) from `https://github.com/loch-sh/lambda-runtime/releases/download/<version>/loch-lambda-runtime-linux-<arch>`, over HTTPS, redirects allowed only to `github.com`, `objects.githubusercontent.com` and `release-assets.githubusercontent.com`. Like the panel, the CLI discovers the newest release on GitHub (at most once an hour, offline: the newest known one), starts from the v1.0.1 sha256 it embeds (host-panel-cli `internal/fndev/runtime-trust.json`, compared by a test with this panel's `legacyRuntimeReleases`) and records in its cache (`releases.json`) the sha256 of every release whose download matched its `SHA256SUMS`: sha256 and size are checked before the file is kept in `$XDG_CACHE_HOME/loch/lambda-runtime/<version>/` (else the OS cache directory; 0700 directories owned by the user, file 0555), re-hashed on every run and again right before the container starts or restarts (a narrowed window, not a closed one). `loch functions dev runtime` shows the binary used, its origin and the `cosign verify-blob` / `gh attestation verify` commands; `--print-compose` never downloads.

**Keep `loch functions dev` in sync**: the CLI duplicates the runtimes and versions (`RuntimeVersions`), the name and handler rules, the reserved names and variables, the limits (and the default `FUNCTIONS_MAX_MEMORY`, 3008 MB, above which it warns), the tmpfs options, the health check timings and every fixed setting of `Render`. Its golden test starts from a copy of `testdata/compose-nodejs.golden.yaml` (host-panel-cli `internal/fndev/testdata/panel-compose-nodejs.yaml`) (and `compose-python-egress.golden.yaml`, `compose-provided.golden.yaml` for the custom runtime) and fails on any difference other than the documented local ones; another test compares those copies byte for byte with this repository's files when it is checked out next to the CLI (or `LOCH_API_REPO`). When `Render` changes, copy the new golden files there and update `internal/fndev`.

## Audit events

`function created`, `function updated`, `function deleted`, `function purged`, `function deletion incomplete`, `function code deployed` (zip sha256, size, file count), `function code deployment failed`, `function deployment activated`, `function deployment rolled back`, `function secret set` / `function secret removed` (name only), `function credentials rotated`, `function network attached` / `function network detached`, `function egress toggled`, `function action` (start, stop, restart), `function verification refused`, `function runtime installed` (version, architecture, sha256, source `download` or `directory`; user `panel`), `function runtime refused` (a binary that does not match the table, or `stored binary modified`), `function invoked` (aggregated per minute; the user of a scheduled invocation is `cron:<task id>`). Successful invocations write no per-request `api request` entry (refusals still do).

## Not done yet (later parts)

- Interface, CLI and MCP (the API side of cron `function_invoke` exists).
- Runtime updates (dated image tags, version discovery), Infisical secrets, public HTTP.
- A per-function disk quota (today the kept deployments bound the disk: `FUNCTIONS_DEPLOYMENTS_KEPT` × 250 MB).

## Trademarks

AWS, AWS Lambda and Amazon Linux are trademarks of Amazon.com, Inc. or its affiliates. Loch.sh is not affiliated with or endorsed by Amazon. These names are used only to describe compatibility: functions run handlers written for AWS Lambda, in the base images published by AWS, through loch's own implementation of the Runtime API. The functions feature of Loch.sh is called "Functions"; it is not an Amazon product.
