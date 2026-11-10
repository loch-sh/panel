# Functions security (work in progress)

> **Work in progress.** The API side of the module exists, with the download of the runtime binary (no function can run before a trusted runtime binary is installed). Usage is in [Functions](functions.md); the plan is in [the decision log, §20](design/services-decisions.md).

## Threat model

| Actor | Can | Must not be able to |
|---|---|---|
| **Function code** (uploaded by an admin, possibly malicious or compromised) | Run inside its container, as uid 10001, within its limits; reach the members of its networks | Escape the container; write outside `/tmp`; reach the panel, the `services` network or the Internet unless egress is enabled; change its own code or runtime |
| **A crafted zip** | Be uploaded | Write outside its deployment directory, exhaust the disk, the memory or the CPU of the panel |
| **CI** writing in `HOSTING_STACKS_DIRECTORY` | Edit `compose.yaml` of a function | Make the panel run it, or run another container under the function's name |
| **A container on a function network** | Call the function's endpoint | Invoke it without its credential (the runtime requires a bearer token or, when enabled, SigV4) |
| **A holder of a container read scope** (`containers:read`, restricted profile) | List containers | Read a function's secrets (`GET /docker/{id}` masks them), open a shell, copy files, tail files, proxy into it, run a cron command in it |

Out of scope, as for the rest of the panel: root on the host or the Docker socket.

## Code upload

`internal/functions/zip.go`, tested with generated and hand-crafted archives (`zip_test.go`):

- **Streamed** to a 0600 temporary file in `fn-<name>/uploads/`, hashed while written, bounded by `FUNCTIONS_MAX_CODE_SIZE`; free space is checked before (zip + 250 MB); a bounded number of uploads at a time.
- **Central directory pre-scan**: `archive/zip` reads every directory record into memory and only compares the declared count modulo 65,536. Before it runs, the panel walks the directory without allocating, from **every** start `archive/zip` could use (the 32-bit end record and any zip64 record, whatever the disk fields of its locator say, each with and without a base offset), keeps the highest count, and refuses more than 50,000 records. It never tries to guess which record `archive/zip` will trust, so a decoy record (a zip64 locator `archive/zip` ignores, declaring one entry) cannot lower the count.
- **Directories**: explicit and implied ones (every parent of a path) count against 20,000, so that 50,000 deeply nested paths cannot create millions of directories from a small zip.
- **Names**: valid UTF-8, no control character (C0 and C1), no backslash, no leading `/`, no empty, `.` or `..` component, at most 255 bytes per component, 4096 in total and 64 levels. Names must already be clean, so two entries cannot collide after cleaning; duplicates are refused, as are a file or a symlink that is also the parent of another entry.
- **Types**: regular files, directories (name ending with `/`, no data) and symlinks only; devices, pipes and sockets are refused.
- **Symlinks**: the target is a relative path (no `/` prefix, no backslash, no empty component). It is resolved **through the tree as the kernel would**, following other links (at most 40 hops), and must stay inside the code at every step and end on an existing entry. A lexical check would not do: with `d/up -> ..`, `d/esc -> up/..` is lexically `d` but resolves above the root. Symlinks are created after every file, so nothing is ever written through one.
- **Compression and encryption**: stored and deflate only; encrypted entries (traditional or strong) refused.
- **Limits on bytes actually written**, never on declared sizes: 250 MB per file and in total, ratio 100:1 per file and for the whole archive (sizes under 1 MB exempt), 2 minutes (checked while validating links, creating directories and writing files). A file whose written size differs from its declared size is refused.
- **Free space and inodes** are reserved per file system for the worst case of every running upload, so concurrent uploads do not count the same free space twice.
- **Extraction through `os.Root`** into a temporary directory of `deployments/`, files created with `O_EXCL`; then the modes are normalized (directories 0555, files 0444 or 0555 when any execute bit was set; setuid, setgid and sticky dropped; world-readable because the function runs as uid 10001 and the files belong to the panel), the tree is hashed, and the directory is renamed to its deployment id.
- **Tree hash**: for every entry sorted by path, its path, type, normalized mode and sha256 (or link target). It is recorded in `function.json` with the sha256 of the zip, and checked (with the read-only modes) before every start, activation and rollback (`Paths.VerifyDeployment`).

Every directory and file of the module (data directory, `.loch`, `fn-*`, `deployments`, the extracted tree) must be owned by the panel and not writable by its group or others; it is checked before each use. Function names are a validated type (`Name`): no unvalidated string reaches a path.

Not done yet: a per-function disk quota (the kept deployments bound it: `FUNCTIONS_DEPLOYMENTS_KEPT` × 250 MB).

## Runtime binary

`internal/functions/runtimebin.go`:

- **The trust root is the panel build**: `runtime-trust.json`, embedded with `go:embed`, lists version, architecture, sha256 and size of the trusted `loch-lambda-runtime` releases (v1.0.1 today). Nothing fetched at run time can add to it: no `SHA256SUMS`, signature or key is downloaded or trusted by the panel. The hashes were checked by hand before they were added (SHA256SUMS, its cosign keyless signature by the release workflow `loch-sh/lambda-runtime/.github/workflows/ci.yml@refs/tags/<version>` with the GitHub Actions OIDC issuer, and the GitHub build provenance attestation of each binary: [procedure](functions.md#adding-a-release-to-the-trust-table)); the `signing` object of the table records that identity, its form is validated, it is never used at run time. Compromising a GitHub release therefore cannot make the panel run another binary; compromising the panel build (or its repository) can.
- Stored in `FUNCTIONS_DATA_DIRECTORY/.runtime/<version>/<arch>/loch-lambda-runtime`: `.runtime` 0700, the version and architecture directories 0755, the file 0555, all owned by the panel. Written to a temporary file, hashed **from the opened descriptor**, checked to be a Linux ELF executable of the architecture, then linked to its final name (never replacing an existing binary). A stored binary is never rewritten.
- Install and verification are serialized (a binary being linked briefly has two links).
- **Re-verified before every start** (`RuntimeStore.Verify`): still trusted by this build and equal to the sha256 pinned in `function.json`, modes, owner, a single link, no symlink on the path, ELF architecture; the image architecture must be the binary's (`CheckImageArch`).
- **Installation** (`RuntimeProvider`, `runtimefetch.go`): the newest trusted release for the host architecture, at startup in the background (retries with backoff) and on demand (an operation needing a missing binary gets `FUNCTION_RUNTIME_UNAVAILABLE` and starts an attempt, at most one at a time and one per minute). Sources: the GitHub release (`FUNCTIONS_RUNTIME_DOWNLOAD`, default) or a local directory (`FUNCTIONS_RUNTIME_SOURCE_DIR`, offline). Both go through `RuntimeStore.Install`: streamed to a temporary file, capped at the size of the table (a larger `Content-Length` is refused before reading), size and sha256 compared, ELF architecture checked; a mismatch is never stored and is audited (`function runtime refused`), a success too (`function runtime installed`: version, architecture, sha256, source). A network failure or a timeout is not a refusal (logged, retried). An existing file is never replaced, even when it fails verification (audited `function runtime refused`, reason `stored binary modified`). After a refusal, no on-demand attempt for 30 minutes (the startup backoff is unchanged); an attempt is marked running under the lock, so a burst of requests starts one only. The state (`idle`, `installing`, `installed`, `unavailable`, `untrusted`) is set on every exit path.
- **Source directory**: owned by the panel's user or root, not writable by group or others, the runtime files regular (checked at startup: the API refuses to start otherwise, and again before every read). The file is opened `O_NONBLOCK|O_NOFOLLOW` and its descriptor must be a regular file (a FIFO would otherwise block the installer forever); the read honours the context.
- **Interrupted installs**: stale `.incoming-*` temporary files (a crash between the link and the removal leaves the binary with two links, refused) are removed under the store lock before the link count is checked.
- **Verification cache**: `Latest` (creation, `GET /functions/config`) reuses a hash check for 10 s when the file's device, inode, size, mode, link count, mtime and ctime are unchanged (any difference, or a failure, drops the entry). `Verify`, the check before every compose run, always re-hashes.
- **Download client**: its own `http.Client` (never the cron anti-SSRF transport, which serves another purpose), TLS 1.2 minimum, 5 min overall timeout, response header timeout 30 s; the URL and every redirect must be `https` to exactly `github.com`, `objects.githubusercontent.com` or `release-assets.githubusercontent.com` (default port, no user info, at most 5 redirects). The proxy variables (`HTTPS_PROXY`) are honored: the proxy sees the destination host only, the content is checked anyway.

## Container hardening

Rendered by `internal/functions/compose.go` (golden files in `testdata/`):

- user `10001:10001`, `read_only`, `cap_drop: [ALL]`, `security_opt: [no-new-privileges:true]` alone (the default seccomp and AppArmor profiles stay), `ipc: private`;
- `/tmp` tmpfs `size=<n>m,exec,nosuid,nodev` (executable as on Lambda), `shm_size` 64 MB;
- `mem_limit` = `memswap_limit`, `cpus`, `pids_limit` 128, ulimits `nofile` 1024 and `core` 0;
- `json-file` logging with rotation (10 MB × 3): Docker's default never rotates;
- the runtime binary and the active deployment as read-only bind mounts (`rprivate`), from paths computed by the panel; secrets and the credential checks through two `env_file`s (`format: raw`, 0400) in the private directory;
- no published port, `restart: unless-stopped`, a healthcheck that does not invoke the function (`/loch/lambda-runtime health`).

No `nproc` ulimit: it counts the processes of uid 10001 across the whole host (every function, and Loch S3 instances, share that uid); `pids_limit` bounds each container.

## Container verification

`VerifyContainer` (`internal/functions/verify.go`) decides whether a running container is the function's, before the panel uses it. Labels can be forged by any stack, so the configuration itself is compared with the reference:

- exact name `fn-<name>`; labels exactly the rendered ones, the image's (same values) and `com.docker.compose.*`; no `traefik.*` label but the rendered `traefik.enable=false` (a router label would publish the function);
- image: the pinned reference, pulled by its digest (`RepoDigests`), the image ID observed at the pull, the architecture of the runtime binary;
- entrypoint `["/loch/lambda-runtime"]`, command `-- <image entrypoint> <handler>`, user, working directory of the image;
- environment: the image's, the rendered variables with their exact values, the secrets and the credential variables (`LOCH_RUNTIME_TOKEN_SHA256`, plus `LOCH_RUNTIME_ACCESS_KEY` only when SigV4 is enabled) by name, nothing else (`LD_LIBRARY_PATH` only as the image sets it; a plaintext `LOCH_RUNTIME_TOKEN` is refused);
- healthcheck, restart policy, CPU limit (within 0.001 CPU: compose parses `cpus` as a float32), logging driver and rotation, tmpfs and `/dev/shm` sizes, memory, swap, CPU, PIDs, ulimits;
- no privilege, capability added, other security option, host or container namespace (network, PID, IPC other than private, UTS, user, cgroup), device, device rule, sysctl, extra host, DNS setting, extra group, cgroup parent, link, `volumes_from`, storage option, other runtime, init, OOM-killer change or published port;
- mounts: exactly the two read-only binds (runtime binary, active deployment), from the computed paths;
- networks: exactly the reference's, by Docker network ID (the registry's), with the function's alias, plus its own egress network when enabled. The egress network is inspected: its ID is the endpoint's, it is a local, non-internal, non-attachable `bridge` without driver options (but the IP family ones recent Docker versions record, measured on 29.9: `enable_ipv4=true`, `enable_ipv6=false`) created by the function's compose project (`com.docker.compose.project` and `com.docker.compose.network` labels, no other label), and the function is its only container.

`ComposeIntact` reports whether `compose.yaml` is byte for byte what the panel renders (with the managed marker); `CheckArtifacts` re-verifies the runtime binary and the deployment before a compose up.

## Choke point

Every compose run of a function goes through one function of the manager (`apply`): runtime binary re-hashed and deployment tree re-verified (`CheckArtifacts`), pinned image checked (digest in `RepoDigests`, recorded ID, architecture), compose project refused unless byte for byte the rendered one (`ComposeIntact`), files written, `up --pull never` with the rendered bytes on stdin (`-f -`: CI cannot swap the file between the check and the run), container verified (`VerifyContainer`) and the project required to hold exactly that container (another one started by hand in the project would share its networks). Every refusal is audited (`function verification refused`, reason only). On failure the files are restored and the previous configuration started again. Stop, delete and purge never run compose: containers are stopped or removed through the Docker API, matched by project label, working directory or name (labels can be forged: every match is removed, none is trusted).

## Secrets and credentials

- **Secrets**: names validated and reserved names refused (see [Functions](functions.md#variables-and-secrets)), values one line of UTF-8; written to `secrets.env` (0400, `format: raw`, private directory), never to `compose.yaml` nor the reference (names only). The value is never returned (`PUT` answers 204), logged, audited (`function secret set` carries the name) nor part of an error message. `GET /functions/{name}` lists the names.
- **At rest**, secrets are plaintext in the private directory: compose must boot without the panel. Backups of `FUNCTIONS_DATA_DIRECTORY` carry them.
- **Invoke credentials** are generated from `crypto/rand` and shown once (`Cache-Control: no-store`). Any variable of the container is readable by the function code (same uid: `/proc/<pid>/environ` of every `docker exec`'d process), so the bearer token never enters it: the container gets its SHA-256 only (`LOCH_RUNTIME_TOKEN_SHA256`), and the panel keeps nothing else. The SigV4 key pair is off by default; when enabled, the runtime needs the secret itself, so it is in the container, readable by the function code: it only lets the function invoke itself, documented and shown in the configuration (`sigv4`). The access key id is random (never derived from the name) and treated as a secret. `Credentials` never prints its values (`String`, `GoString`).
- The panel's own invocations and the healthcheck run on loopback inside the container and need no credential.

## Invocation

- Through `docker exec … /loch/lambda-runtime invoke --watch-stdin --log-tail` (`docker.Client.ExecInvoke`) into the container ID that was just verified, never through `NetworkBridge`: the panel never joins a tenant network (no exposure of the panel API to functions, no DNS impostor through `--network-alias`).
- The container is verified (`VerifyContainer`) before each invocation; a modified, stopped or unverified function is never invoked.
- Bounds: event one JSON value of at most 6 MiB (`MaxBytesReader`; read only once the function is found and a slot taken, within 30 s of the first read: a connection read deadline, 408 `FUNCTION_EVENT_TIMEOUT`), followed by a newline (the runtime's decoder cannot finish a top-level scalar such as `42` before the next byte, and stdin stays open), stdin kept open after the event and closed with the exec connection (the runtime's invoke exits then: Docker closes an exec's stdin, never its stdout, when its client goes), stdout capped at the envelope maximum **before** buffering (beyond: an error, nothing kept), stderr 4 KiB, deadline = function timeout + 30 s + 15 s (the connection is then closed; Docker cannot kill an exec: the invoke subcommand bounds itself with a shorter deadline). At most 2 invocations per function in flight from the panel.
- The envelope is parsed strictly (`ParseEnvelope`: known fields only, bounded base64, request id and error type patterns) and answered as a fixed JSON document: `application/json`, `nosniff`, `Content-Security-Policy: default-src 'none'; sandbox`, `no-store`; payload as a JSON string (escaped) or base64, log tail with invalid UTF-8 replaced. No header or content type of the function is ever copied (stored XSS on the panel origin otherwise).
- Audit: aggregated per function and minute, never content; written once more after the server stopped, and invocations still ending later are written one by one; the per-request `api request` entry is skipped for successful invocations only (status < 400): refusals and failures are recorded one by one.

### MCP opt-in (`mcpInvocable`)

A function can be invoked by the loch MCP tool only when its `mcpInvocable` flag is on (off by default; set at creation or by `PATCH /functions/{name}`, both needing `functions:deploy`, since it widens who can trigger the function's side effects, and audited in `function updated`). **This is a guard-rail, not an access control.** The API cannot tell an MCP client from any other caller: `POST /functions/{name}/invoke` answers every holder of `functions:invoke` whatever the flag, and the check is made by the MCP tool of the CLI before it calls the API. It protects against an assistant invoking a function it was not meant to touch (a prompt injection, a confusion between functions); it does not protect against a compromised client or a script with the same API key. The access control is the API key: give the MCP client a key without `functions:invoke` when no function should be invocable from it.

## Uploads

The API server has no read timeout (streamed uploads): a client stalling or trickling a zip would hold the function lock and an upload slot. Each read of an upload refreshes a connection read deadline (15 s without progress aborts a blocked read), and after 30 s the average rate must stay above 32 KiB/s; otherwise the upload is cut with `FUNCTION_CODE_TIMEOUT` and the connection closed (the server does not read the rest of the body). The extraction itself is bounded to 2 minutes.

## Scopes

Variables, handler, networks and egress decide where the function code can send its secrets (a variable pointing at an attacker's host, egress on): they need `functions:deploy`, like the code and the secrets, not only `functions:write`. Attaching a function to a network or detaching it through the networks routes needs `functions:deploy` besides `networks:write`. The MCP opt-in (`mcpInvocable`) needs `functions:deploy` too: it widens who can trigger the function's side effects.

## Locks

Lock order: networks (`networks.Manager`'s operation lock, bounded to 5 minutes), then a function's lock. `PUT /functions/{name}/networks` validates the networks without the networks lock (a network deleted meanwhile makes the recreation fail and the change roll back). A function's creation creates its network before taking its lock (the network creation is atomic and reserves the name); a deletion removes the network after releasing it.

## Function containers in the Docker views

- **Masking is central**: `docker.Client.InspectContainer`, the inspect path of `GET /docker/{id}`, the WebSocket and the MCP tools (through the API), masks every environment value of a function container (`NAME=***`, names kept). A container is treated as a function's when it carries the `sh.loch.function` label, or its name or compose project starts with `fn-`; a container wrongly matched only loses features. The functions module uses `InspectContainerRaw` for its own verification.
- **Protection**, like the panel's own container (`IsSelf`): shell (WebSocket `attach`), `GET`/`PUT /docker/{id}/cp`, file tail (REST and WebSocket), `loch proxy` (`proxyOpen`), cron `docker_exec` and `docker_action` (at creation, update and run), `POST /docker/{id}/{action}` and `POST /stacks/fn-*/{action}` are refused with `FUNCTION_CONTAINER_PROTECTED` (the functions API starts and stops them after verifying them); stdout logs stay readable by admins.
- **Lists** (`GET /docker`, domains, stack actions) drop function containers for restricted users and from stack actions with the same rule (`docker.IsFunctionSummary`: label, name or project).
- **Restricted profiles** never reach a function container: one shared check (`docker.UserCanAccess`) for the HTTP and WebSocket paths (logs, stats, file tail, shell, proxy, cp, inspect), by name, label and project; `fn-*` names are never granted (`PATCH /users/{email}` refuses them) and stored ones are dropped when the users are loaded.
- **Matching is deliberately broad**: a container whose name or compose project starts with `fn-` is treated as a function's even if the panel did not create it; it only loses features (fails safe).
- Not covered: whoever holds the Docker socket or root on the host (out of scope, as for the rest of the panel).
