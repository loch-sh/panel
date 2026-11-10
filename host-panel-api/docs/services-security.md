# Managed services security

How the services module (databases, cache, messaging) protects tenants from each other, the host from tenants, and the panel from a compromised CI. How to use the module is in [Managed services](services.md); the rest of the API is covered in [Security](security.md).

## In short

- **Tenants get credentials, nothing more.** They reach their own resource over the network, without any account able to administer the engine.
- **Passwords are generated, shown once, never stored in clear**, except where the protocol requires it (S3 SigV4: see Loch S3 below). 190-bit random passwords; the panel keeps digests or nothing.
- **The panel only runs what it wrote.** Files edited by hand, including by CI, are detected; such an instance is never started by the panel.
- **The panel only talks to the container it started.** Before sending a command or a password, it checks the container field by field.
- **Every engine is configured restrictively.** No administration commands for tenants, no plugin or module that is not needed, admin interfaces on the loopback only.
- **Publishing a port is bounded by the server configuration.** An admin chooses it per instance in the panel (dedicated scope `services:expose`), but only on host addresses the environment allows, with TLS required on public addresses.
- **Networks separate the sites from each other**, not from whoever can use the Docker socket: see [Private networks](networks.md#limits-and-what-a-network-is-not).

## Threat model

| Actor | Can | Must not be able to |
|---|---|---|
| **Tenant** (a hosted site, or anyone holding its credentials) | Connect to its own resource, from its containers on the shared network or through a published port | Reach another tenant's data; run commands, write files or open connections from the engine; exhaust a shared instance; learn the admin credentials |
| **CI** deploying stacks in the stacks directory, shared with the panel | Write, rename or replace files and directories there | Make the panel (running as root) run a compose file or a container it did not write; choose a tenant's password; change an instance's version or limits |
| **Network attacker** | Reach published ports | Read credentials in transit on a public address; reach an admin account |
| **Stolen admin session or API key** | Create, change and delete resources, networks and exposures within its scopes | Publish an instance anywhere the environment does not allow; open a port without `services:expose`; make an unauthenticated engine reachable from the shared network or beyond the loopback |
| **A container on a panel network** (a CI stack, a compromised site) | Join any network by name, if it can use the Docker socket; connect to what is on its networks | Be trusted by the panel (it is shown as `unmanaged` and can be disconnected); pass for a member of a network, or for an instance |
| **Panel admin** | Create, rotate and delete resources | See a password again after it was shown; change the audit trail from the panel |

Out of scope: someone with root on the host or access to the Docker socket controls everything.

## Topology

The topology follows the measured footprint of the engines and their native tenant isolation:

| Engine | Idle memory | Native isolation | Topology |
|---|---|---|---|
| PostgreSQL, MariaDB, MySQL, MongoDB | heavy | strong: databases, roles, grants | **shared**: one instance per version, a database and a user per resource |
| RabbitMQ 4.2 | 184 MB, 57 processes | strong: virtual hosts, permissions, limits | **shared**: a virtual host and a user per resource |
| Redis 8.4 / Valkey 9.0 | 12 / 18 MB | weak: one keyspace, global commands, one `maxmemory` | **dedicated**: one container per resource |
| NATS 2.12 | 11 MB | accounts, but file configuration and reloads | **dedicated**: one container per resource |

## Credentials

- **Names** match `\A[a-z][a-z0-9_]{0,31}\z`, anchored with `\z` so that a trailing newline is refused. A valid name is safe in every identifier syntax.
  - Dedicated instance names become host names: `\A[a-z][a-z0-9]{0,23}\z`, with no underscore.
  - MySQL 5.6 limits user names to 16 characters.
- **Reserved names**:

  | Scope | Names | Prefixes |
  |---|---|---|
  | Every engine | `admin`, `all`, `default`, `healthcheck`, `loch`, `none`, `root`, `test`, `user` | `loch_` |
  | PostgreSQL | `postgres`, `template0`, `template1`, `public`, `information_schema`, `repl`, `replicator`, `current_user`, `session_user` | `pg_` |
  | MariaDB, MySQL | `mysql`, `sys`, `information_schema`, `performance_schema` | `mariadb`, `mysql`, `sys_` |
  | MongoDB | `admin`, `config`, `local` | `system` |
  | RabbitMQ | `guest` | `amq` |

- **Passwords**:
  - 32 characters out of 62 (about 190 bits), from `crypto/rand` by rejection sampling, so that every character is equally likely;
  - never chosen by users: every engine refuses a password that does not have the generated form;
  - shown once, in the creation or rotation response (`Cache-Control: no-store`);
  - never logged, never returned by the CLI's MCP tools, never stored in clear by the panel, except the Loch S3 secret (SigV4 signs with the secret itself, so the server must know it: kept in the private directory, see below);
  - tenant passwords travel on standard input or are rendered as digests, never on a command line nor in an environment variable.
- **Admin accounts** of shared instances cannot be used from the network:

  | Engine | Network restriction |
  |---|---|
  | PostgreSQL | `postgres` rejected over the network (`pg_hba`) |
  | MariaDB, MySQL | `root` limited to `localhost` |
  | MongoDB | `root` limited to the loopback addresses |
  | RabbitMQ | administered with `rabbitmqctl` through the local Erlang distribution, no admin user |

  - Their password is a file in the container (`secrets/admin_password`, mode 0400, in a 0700 directory), read by the engine at first start.
  - The panel's admin commands read it from that file inside the container. With MongoDB it then appears in the `mongosh` arguments, and with MySQL in the client's environment (`MYSQL_PWD`), visible only inside the container.
- **Rotation** closes the user's open connections: the old password stops working immediately. Dedicated instances are restarted.
- **Deletion** is final. `SERVICES_ARCHIVE_ON_RETIRE` renames the data of a removed instance (shared or dedicated) instead of deleting it.

## What the panel trusts on disk

The stacks directory is writable by CI: nothing read from it is trusted.

### Instance directory (`HOSTING_STACKS_DIRECTORY/svc-<instance>/`)

- **Marker.** Only directories carrying the panel's `.loch-managed` marker and a valid `x-loch` manifest for an enabled engine and version are managed.
  - Names and paths (project, container, data directory) are derived from the instance key on the server side, never read back.
- **Integrity check** before any compose command. These must be byte for byte what the panel renders:
  - `compose.yaml`;
  - `compose.override.yaml`, rendered again from the exposure recorded in the private reference;
  - the generated configuration files.
- **Presence only**:
  - the TLS certificate and key must be regular files;
  - the admin secret must exist.
- **Manifest version.** A manifest declaring another layout version is reported as modified: a layout change will rewrite the files, never trust them. Version 2 (private networks) made instances created by an earlier development build "modified": they must be purged (no migration, the module was not in production).
- **Modified instances.** An instance that fails the check is shown as "Edited by hand" and never started by the panel.
  - It can only be deleted with all its data (**purge**): its files are never run, not even `docker compose down`. The panel removes, through the Docker API, the containers of its project (by the panel's project name, or started from the instance directory under another name), the publication network, then the data (archived with `SERVICES_ARCHIVE_ON_RETIRE`), the private directory and the instance directory, all at the paths the panel computes, never at paths read from the edited files. Audited as `service instance purged`.
  - When one of its ports must be closed, it is stopped instead.
  - It is not retired automatically.
- **Writes.** Files are written atomically (temporary file, then rename), and never through a symbolic link: directories are checked with `RealDir`, files opened with `O_NOFOLLOW`.

### Private directory (`SERVICES_DATA_DIRECTORY/.loch/<project>/`)

For every instance (shared ones included, since the networks), the panel keeps two things out of reach of CI, in a directory only root can open (0700):

- **`instance.json`**: the trusted record of the instance (engine, version, name, options, **networks** and **exposure**).
  - A manifest whose version or options differ from it is reported as modified, and the compose files must be what is rendered from its networks and exposure. Without this check, a consistent rewrite could run another version on existing data or lift the memory limit.
- **`state/`** (instances with a password): the password digests read by the engine (SHA-256 for Redis and Valkey ACLs, bcrypt for NATS).
  - Whoever writes them chooses the password: this is why they are not in the stacks directory.
  - The directory (0755) and its files (0644) are readable by the engine user through a read-only mount; the 0700 parent protects them on the host.
  - **Loch S3**: `state/users.json` holds the secret in clear (SigV4 needs it). It is written 0400 and owned by the container user (10001); `password.digest` (0600, root) holds the same secret so that the file can be rendered again when the options change. When the API does not run as root (development), the file cannot be given to the container user: it is left 0444 only if `.loch/` and the instance's private directory are both closed to other users (0700), and refused otherwise.

### Data (`SERVICES_DATA_DIRECTORY/svc-*`, mode 0700)

- **Location**: outside the stacks, the sites and SFTP. The configuration refuses overlapping directories.
- **Reuse**: creating an instance refuses a non-empty data directory, which would bring back users whose passwords nobody knows.

## Container verification

Before sending a command or a password to a container, or restarting it, the panel checks it is exactly the one it started. Compose labels can be set by any stack: a look-alike container would otherwise receive the passwords.

| Checked | Expected |
|---|---|
| Name, image, command | the instance's. Shared instance: the image by tag. Dedicated instance: the image recorded in its private reference, pinned by digest: the container is created from `repo:tag@digest` (or from the tag alone, before it was pinned) and the image it runs, inspected by its content ID, must carry that digest for the repository of the tag (see [Version discovery and updates](#version-discovery-and-updates)) |
| Labels | the panel's instance label and the compose working directory |
| User | the engine's, when it sets one |
| Entrypoint and environment | the image's, plus the engine's environment; nothing else (no added `PATH` or `LD_*`) |
| Security options | exactly `no-new-privileges:true`; not privileged; no added capability |
| Hardened engines (Redis, Valkey, NATS) | every capability dropped, read-only root filesystem |
| Host access | no host network, PID, IPC or user namespace; no device, sysctl, DNS or host entry, extra group, link or cgroup parent; runtime `runc` |
| Limits | memory, swap (equal to memory, or `-1` without swap accounting), CPUs, processes |
| Mounts | exactly the expected bind mounts and tmpfs; only the data mount is writable |
| Ports | exactly the published ports |
| Networks | exactly the networks of the reference, no more and no fewer, each with the Docker ID the panel registered (a network of the same name created by a stack is refused), the instance alias on each, and the instance's own publication network when published. A container without network settings is refused |

The live networks are checked too, before the container is trusted: ID, `internal`, local `bridge`, panel labels, no compose project label, registered subnet. A container that does not match is refused (the instance is "not ready") and the reason is logged.

## Networks and exposure

### Private networks

How to use them is in [Private networks](networks.md); the decision is in [the decision log](design/services-decisions.md#18-private-networks-2026-11-05).

- **Native Docker.** A network is an internal `bridge` network created by the panel, referenced `external` by the compose files. The registry (`DATA_DIR/networks.json`) holds the Docker ID, subnet and labels the panel verifies; the stacks never need it.
- **Membership is recorded with the member.** Instances in their private reference (`instance.json`, outside the stacks directory that CI can modify), sites in the `x-loch.networks` of their compose file. The compose files must be exactly what the panel renders from it; a file edited to add a network makes the instance "modified" and it is no longer run.
- **Name collisions.** The panel never adopts a Docker network by name. Any stack can create a network with a given name, so containers are trusted only when attached to the network with the registered Docker ID.
- **Startup reconcile.** A missing network is recreated with the same subnet; one whose ID or properties differ is reported as compromised, and attaching to it is refused.
- **System network `services`** (`SERVICES_NETWORK`, `loch-services`): created `internal`, with an existing network adopted only if the panel created it (label) and it is an internal bridge; otherwise the API says to remove it. The name cannot be `bridge`, `host`, `none`, `default`, the Traefik network or a `loch-net-*` name. It cannot be deleted. Engines without authentication are refused on it.
- **Hosted sites** on any panel network lose `NET_RAW` (no ARP spoofing of the traffic of the others).
- **Detaching** disconnects the container first and recreates it; a failure stops the instance and never reconnects it. An instance keeps at least one network, because Compose would otherwise give it a default network with Internet access.
- **What it does not do.** A network is not an access control against CI stacks or anything that can use the Docker socket: a container can join a network by name. The panel lists the members it does not manage and lets an admin disconnect them. Containers on several networks bridge them at the application level only (no `NET_ADMIN`, no forwarding).

### Publishing a port

Chosen per instance in the panel (`PUT /services/instances/{instance}/exposure`), not in the environment any more: this reverses decision D10 ("the web interface allows no change", see [the log](design/services-decisions.md#18-private-networks-2026-11-05)). The exposure is bounded by a policy that only the environment can change, so a stolen session cannot publish an instance anywhere:

- **Scope**: `services:expose`, separate from `services:write`; no MCP tool.
- **Addresses**: `SERVICES_EXPOSE_ALLOWED_CIDRS` (default `127.0.0.1`) lists host bind addresses, literal unicast addresses only (no link-local, multicast or IPv4-mapped). `0.0.0.0` is accepted only when listed alone, never through a range. A non-loopback entry requires `SERVICES_ALLOW_PUBLIC_BIND` and logs a warning: Docker bypasses the host firewall and cannot filter the clients of a published port, so filter sources in the `DOCKER-USER` chain.
- **Ports**: 1024 to 32767, except 2375, 2376, 2538, 8080 and the API and SFTP ports. Two instances cannot share a port, and a port already published by another container is refused.
- **Loopback only** for out-of-support versions, for engines without authentication, and for engines that cannot require TLS while `SERVICES_TLS=auto` (MongoDB).
- **Recorded** in the private reference; the override is rendered from it.
- **TLS** (`SERVICES_TLS=auto`): an instance published on a public address requires TLS from every client, the sites included, on the same port. Plain connections are refused.
  - **Protocols**: TLS 1.2 and 1.3.
  - **Certificate**: self-signed (ECDSA P-256, one year, renewed 30 days before expiry, SHA-256 fingerprint shown to clients), or provided with `SERVICES_TLS_CERT_FILE` / `SERVICES_TLS_KEY_FILE`.
  - **Key**: readable by the engine user only (0400, in a 0700 directory).
  - `SERVICES_TLS=off` disables TLS deliberately and logs a warning.
  - **Optional TLS** (`SERVICES_ALLOW_TLS_OPTIONAL`, then per exposure) offers TLS without requiring it (PostgreSQL: `ssl=on` with the usual `host` lines; MySQL 8.4 and MariaDB: certificate without `require_secure_transport`). Plain clients, sites included, then send passwords and data in clear on that port: an explicit choice per exposure, recorded in the override (`tlsOptional`), shown in the API and the interface.
  - MongoDB cannot require TLS yet.
- **Applying exposure** happens at each change, at startup, then every day:
  - **Fail-closed**: closing or narrowing a port that fails stops the instance instead of reopening the previous port. A failed opening keeps the previous exposure.
  - **No retry loop**: a failed configuration is recorded (`.loch-exposure-failed`) and not retried automatically until it changes; an explicit change from the panel is always tried.
- **Audit**: `service exposure changed` (before, after, TLS optional), `service exposure applied`.

### Client IP and `TRUSTED_PROXIES`

The rate limit and the audit trail use the client IP. `X-Forwarded-For` is read only when the request comes from the loopback or from `TRUSTED_PROXIES`, and from right to left, skipping trusted proxies. The API used to trust every address of `172.16.0.0/12`, which includes every container: a container on a shared network could forge its IP and dodge the rate limit, or put someone else's in the audit trail. The default is now the loopback only; behind Traefik, set `TRUSTED_PROXIES` to the subnet of the Traefik network (see [Configuration](configuration.md#core)).

## Engines

All containers run with `no-new-privileges`, a process limit (512 by default), bounded logs (10 MB × 3), `traefik.enable=false` and `restart: unless-stopped`.

### Databases (shared)

| | PostgreSQL | MariaDB, MySQL | MongoDB |
|---|---|---|---|
| Resource | role and database it owns, mark `loch-managed` | user and database, recorded in `loch_meta.resources` | user with `readWrite` and `dbAdmin` on its database (no `userAdmin`), mark in `customData` |
| Authentication | SCRAM-SHA-256; verifier computed by the panel. Per database, on request: md5 (legacy; hba method `md5`, which still authenticates SCRAM passwords with SCRAM) | MariaDB and 5.6: hash computed by the panel, the password never reaches the server. MySQL 8.4: caching_sha2_password; per database, on request: mysql_native_password (legacy, hash computed by the panel; plugin loaded with `--mysql-native-password=ON`, the default stays caching_sha2_password) | SCRAM-SHA-256 only |
| Tenant restrictions | `NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS`, 20 connections; database closed until `REVOKE ALL ... FROM PUBLIC`; `postgres` and `template1` closed to `PUBLIC` | `GRANT ALL` on its database only, with `_` escaped; 20 connections; `local_infile` off; `symbolic-links` off on 5.6 | own database only |
| Server limits | 200 connections, 10 s authentication timeout; failed statements not logged with their text | 200 connections, `skip-name-resolve`; MySQL 8.4 without binary log | 200 connections; `/data/configdb` tmpfs `noexec,nosuid,nodev` |
| Admin commands | local socket, SQL on standard input, 20 s statement and 10 s lock timeouts | constant `sh -c`, SQL on standard input, 20 s statement and 10 s lock timeouts | single-line scripts; only validated names and generated passwords can be quoted |
| Deletion and rotation | owned objects dropped | user dropped and sessions killed before the database | rotation recreates the user (closes its connections); deletion drops every user and role of the database |

There are no memory or CPU limits on the database containers: size the host for them.

### RabbitMQ (shared)

| Area | Measures |
|---|---|
| Surface | no plugin (the image enables Prometheus, served without authentication); Erlang distribution and epmd on the loopback only |
| First boot | empty definitions: no `guest` user and no default vhost are ever created; anonymous login disabled; `PLAIN` only |
| Resource | a virtual host and a user of the same name, permissions on that virtual host only; mark `loch-managed` (not an administrative tag) |
| Per-tenant limits | 256 connections and 128 queues per virtual host; 256 connections and 128 channels per user; operator policy (tenants cannot override it): queues up to 64 MB, then publishes rejected |
| Node limits | 1 GiB memory (publishers blocked at half), 2 CPUs, 1 GB free disk minimum, 2048 connections, 4 MB messages, 128 channels per connection, 10 s handshake timeout; classic queues by default |
| TLS | TLS 1.2 and 1.3, 5 s handshake timeout |
| Passwords | on standard input, never as arguments; rotation closes the user's connections |
| Consistency | a resource that fails half-way is removed, so that it never blocks its name |

### Redis and Valkey (dedicated)

| Area | Measures |
|---|---|
| Accounts | `default` disabled; no admin account; the panel runs nothing in the container |
| Tenant user | every data command, except `@admin`, `@dangerous` and `SCRIPT DEBUG`: no `CONFIG`, `REPLICAOF`, `MIGRATE`, `DEBUG`, `RESTORE`, `MODULE`, `ACL`, `SHUTDOWN`, `MONITOR`, `CLIENT KILL`… (no file writes, no outgoing connections, no server change); `INFO`, `FLUSHDB`, `FLUSHALL`, `KEYS` and `SORT` allowed for common clients |
| Configuration | `enable-debug-command no`, `enable-module-command no`, `enable-protected-configs no`, `sanitize-dump-payload yes` |
| Options | Lua scripts can be refused (`-@scripting`); Redis 8 bundled modules (JSON, search, time series, probabilistic structures) refused by ACL unless enabled |
| Memory | container memory twice `maxmemory` (persistence forks may double it); `maxmemory-clients 5%`; 16 MB query buffer and bulk length; output buffers limited; 512 clients |
| Healthcheck | Unix socket, no secret: a `NOAUTH` answer proves the server is up |
| TLS | TLS 1.2 and 1.3 only, on the same port (`port 0`) |
| Container | user 999, every capability dropped, read-only root filesystem, 1 CPU, 128 processes |

### ElasticMQ (dedicated, no authentication)

| Area | Measures |
|---|---|
| Authentication | **none** in ElasticMQ: any credentials are accepted. Every site on a network of the instance can use, purge or delete its queues. Marked `Unauthenticated`: no password generated or rotated, warnings in the API (`unauthenticated`), the interface and the CLI. Refused on the shared `services` network: it must be given a private network |
| Surface | SQS API only (`rest-stats` disabled); configuration written by the panel |
| Container | user 10002 (the image runs as root otherwise), every capability dropped, read-only root filesystem with a `/tmp` tmpfs, 1 CPU, 128 processes; queues and messages in the data directory (H2) |
| Exposure | loopback only, always (no authentication to protect a public port) |

### Eclipse Mosquitto (dedicated)

| Area | Measures |
|---|---|
| Accounts | one user, the tenant; no anonymous access; password file with a PBKDF2-SHA512 digest computed by the panel (`$7$`, as `mosquitto_passwd`), 0400 owned by the broker user |
| Limits | 1024 connections, message size and queued messages as chosen, memory limit at three quarters of the container, 20 in-flight messages per client |
| Surface | one listener (IPv4), no bridge, no plugin; logs on stdout (errors, warnings, notices) |
| TLS | required on the same port once published (`SERVICES_TLS=auto`) |
| Container | image user 1883, every capability dropped, read-only root filesystem, 1 CPU, 64 processes |

### NATS (dedicated)

| Area | Measures |
|---|---|
| Configuration | written by the panel (the image's default one opens monitoring to the network and declares a cluster with hard-coded credentials) |
| Surface | monitoring on `127.0.0.1` only (healthcheck); no cluster, gateway, leaf node or system user |
| Accounts | one account holding the tenant user; bcrypt digest at cost 8 (keeps the pre-authentication CPU low; 190-bit passwords are out of reach of an offline search anyway) |
| JetStream | memory store a quarter of the container memory, storage as chosen; at most 100 streams and 1000 consumers |
| Limits | 1024 connections, 10000 subscriptions, 1 MB payloads, 64 MB pending, 4 KB control lines, 2 s authentication timeout, 5 s write deadline |
| Container | user 1000, every capability dropped, read-only root filesystem, 1 CPU, 128 processes |

## Operations

- **Commands in containers**:
  - fixed arguments, never interpolated by a shell;
  - bounded by `timeout -s KILL` and the statement timeouts;
  - outputs capped (1 MiB / 64 KiB), never logged nor returned: they may echo the input.
- **Unknown outcomes.** A command that does not report its end is never taken for a success. The client is told to check and retry; the panel resolves what it can by reading the engine catalog.
- **Concurrency**:
  - one lock per instance, and one per engine for the creation of dedicated instances, so that the instance limit holds under concurrent requests;
  - operations are detached from the request (5 minutes at most), so that an instance is never left half created when a client goes away.
- **Rollback**:
  - a failed creation removes everything it created;
  - a failed rotation restores the previous digests and restarts the instance with them;
  - a failed update of a dedicated instance restores its files and its image tags, and restarts it on the previous image.
- **Limits**:
  - `SERVICES_MAX_RESOURCES_PER_INSTANCE` (1 to 500, default 50) per shared instance;
  - `SERVICES_MAX_DEDICATED_INSTANCES` (1 to 200, default 20) per dedicated engine. Only instances the panel created count (they have a private directory), so CI cannot exhaust the limit.
- **API**:
  - admin only, with scopes `services:read` / `services:write`;
  - browser writes must come from the panel's origin (CSRF);
  - creation bodies limited to 4 KiB with unknown fields refused;
  - every operation recorded in the audit trail, without secrets.
- **Preflight at startup.** Paths must not go through a symbolic link, the stacks and data directories must be disjoint and mounted at the same path in the API container, and the API must run as root (ownership changes are tested).
  - A failure, after three attempts, disables the module without stopping the API (`SERVICES_MISCONFIGURED`), with the reason in the logs.

### Loch S3 (dedicated)

| Area | Measures |
|---|---|
| Accounts | one root user, the tenant; users file rendered by the panel, mounted read-only; no `S3_ADMIN_API_KEY`, `S3_ACCESS_KEY_ID` or `S3_SECRET_ACCESS_KEY` (they would turn on the admin API or switch the authentication mode; the container verification pins the environment) |
| Fail-closed | a missing or invalid users file stops the server (exit 1) instead of opening it or creating a root user |
| Secrets | never in the environment, `compose.yaml` or `docker inspect`; access key derived from the secret |
| Limits | storage quota (`S3_MAX_STORAGE_BYTES`, `QuotaExceeded` once reached), memory, 1 CPU |
| Container | user 10001 set by the panel (the image runs as 100:101 otherwise), every capability dropped, read-only root filesystem with a 16 MB `/tmp` tmpfs, 128 processes; healthcheck on `/_loch/health` (no secret, no figures) |
| Encryption at rest | optional (`encryption=on`, off by default, fixed at creation): every object written is encrypted (SSE-S3, AES-256-GCM, `S3_ENCRYPT_BY_DEFAULT`) with a 32-byte master key generated once by the panel per instance, kept in `state/sse.key` (0400, owned by the container user, read through `S3_ENCRYPTION_KEY_FILE`, never in the environment) and kept by rotations and option changes. The key lives in the private directory, apart from the data directory: a copy of the data alone is unreadable; root on the host reads both. Refused on versions before 1.4, which would ignore it and store objects in clear. Encrypted at rest once a write completes: Loch S3 encrypts the object in place before answering, and multipart parts stay in clear in `.uploads/` until the upload is completed (a crash in between can leave plaintext on disk). Since the option is fixed at creation, there are no objects written in clear before an activation |
| Shared mode | refused: a review of Loch S3 (2026-11-05) found cross-tenant reads and deletions, fixed in 1.3.0; per-user isolation (admin listener, per-user limits, purge) is still missing |
| Logs | method, path and status only (1.3.0; 1.2.0 logged every request header) |
| Bounds | header (30 s) and body (60 s) timeouts, aws-chunked bodies streamed at constant memory, blocking I/O pool capped at 32 threads (`S3_MAX_BLOCKING_THREADS`, below `pids_limit`) |
| Integrity | trailing checksums and `x-amz-content-sha256` not verified by Loch S3: on the cleartext shared network, an on-path attacker could alter uploads (see the clear traffic limitation) |

### Legacy authentication

Offered per database for old clients (`legacyAuth` at creation): MySQL 8.4 `mysql_native_password` (clients without TLS nor RSA key exchange fail with caching_sha2_password: "Authentication requires secure connection"), PostgreSQL `md5` (clients without SCRAM). MariaDB and MySQL 5.6 already use `mysql_native_password`: the option is not offered there.

- **What is lost**: the protection of the exchange. With `mysql_native_password` and `md5`, someone who records a login on the network gets material for an offline search, and md5 verifiers are replayable once stolen from the server. The passwords stay generated (190 bits, out of reach of an offline search); the clear password never reaches the server (hash computed by the panel).
- **Kept**: the mechanism is fixed at creation and kept by rotations (the provisioner reads it from the engine: `mysql.user.plugin`, `pg_authid.rolpassword`).
- PostgreSQL 18 deprecates md5 passwords: they will stop working in a future major version.

## Version discovery and updates

The version catalog and the updates of dedicated instances (see [Managed services](services.md#versions-and-updates)) read what the registries and endoflife.date answer. **Every answer is untrusted.**

| Area | Measures |
|---|---|
| Images | an image reference is only ever built by engine code from a tag that matched the engine's **strict regular expression** (the whole tag, bounded patch number); a tag, a digest or a date from a response is never used as is. A response cannot add an image, a registry or an option |
| Hosts | fixed by the engines (`docker.io`, `ghcr.io`, `mirror.gcr.io`) and by the code (`endoflife.date`). The token realm named by a registry challenge is only followed when it is the registry host itself, `auth.docker.io` or `ghcr.io`; redirects must stay on HTTPS (5 at most), on the same host or a known registry host |
| Bounds | responses capped at 8 MiB, 20 pages and 50,000 tags per repository, 30 s per request, at most 24 digest checks per engine and discovery, a discovery capped at 3 minutes, manual checks throttled to one per 30 seconds |
| Dates and digests | dates must be `YYYY-MM-DD`, digests `sha256:` plus 64 hexadecimal characters; anything else is dropped. The cached `services-catalog.json` is validated again when loaded (enabled engines, tags matching their version, dates, digests) and read without following symbolic links |
| Failures | a failed discovery keeps the previous values and never widens the offer: when the support dates cannot be read, the previous dates are kept rather than offering every version |
| Pull | the digest of the target tag is read on the mirror and the image is **pulled by digest** (content-addressed: what is run is what was checked), and the pulled image must carry that digest. The pull is bounded to 4 minutes |
| Pinned, no tag moved | a dedicated instance runs `repo:tag@digest`: the update renders the new digest, it never moves a local tag. The tag of a version is shared by its instances: moving it would change, at their next recreation, what the other instances run (a rollback could bring one back to an older image, unaudited). A rollback restores the files, and thus the previous pinned reference |
| Running image | after the restart, the container must run the image pulled by digest (its image ID is compared with the pulled one); otherwise the update is rolled back |
| Reference | the image an instance runs and its digest are recorded in its private reference `instance.json` (outside the stacks directory that CI can modify). The image must be one the engine computes for a tag of the instance's version, the digest a well-formed `sha256` digest, and the compose files must be rendered from them: a tampered image or digest makes the instance **modified**, and it is never run. The container verification compares against them. A digest is only ever taken from Docker (the `RepoDigests` of the image pulled for the tag at creation, or of the image the verified container runs when an older instance is pinned at startup), for the repository of the tag, never from a file of the stacks directory |
| Compose without its digest | a compose file rendered without the digest while the reference has one (interrupted pinning, interrupted update, or written back by CI at any time) would run the shared tag: the instance is reported **modified** and no operation runs compose on it. Only the pinning at the next start repairs it, with a digest the verified container's image carries (the recorded one, or the running image's, logged). A pinned instance whose update is interrupted between the reference and the compose write stays modified, like an interrupted option change |
| Several digests | an image may carry several digests for its repository: the container may be created from any of them provided the image also carries the pinned one; an update is offered only when the running image does not carry the mirror digest |
| Scope | admin only, `services:write` (an update recreates the container); the MCP tool only refreshes the image of the current version (a version change cannot be undone: a human decision, like publishing a port); no downgrade, at most 3 newer versions, dedicated instances only; audited without secrets (`service instance updated` with the versions, images, image IDs and digest; `service instance update rolled back` with the reason) |

Residual risks: the mirror is trusted for the content of the images, and could serve an older digest of a tag (no cross-check with the origin registry); there is no data snapshot before a version change; new majors are offered by design without having been tested (no highest tested version per engine).

**Containers page**: the web routing links are built from Traefik labels, which any container can set: only valid DNS host names are linked (no userinfo, path, port or other characters), 10 at most, with `rel="noopener noreferrer"`. The `Service` tag and the stack type filter come from labels too: display only, the API decides what it manages.

## Known limitations

- **Clear traffic on the networks** until an instance is published with TLS: Redis, AMQP and NATS passwords travel in clear on the networks of the instance.
  - The panel's sites cannot sniff it (no `NET_RAW`), but a stack deployed by CI that joins a network can, or can take over an instance's DNS alias. A network is not an access control against CI (see [Private networks](networks.md#limits-and-what-a-network-is-not)): the panel shows such members as `unmanaged` and can disconnect them.
  - Do not let CI stacks join these networks, and prefer private networks to the shared `services` one.
- **The `traefik` network**, shared by every site, still allows `NET_RAW`.
- **Race window (TOCTOU)**: the files are checked again right before `docker compose` runs, but a race between the check and the execution remains possible.
- **Database resources**: no memory or CPU limits on database containers, and no disk quota per instance. Monitor the free space of `SERVICES_DATA_DIRECTORY`.
- **Images**: dedicated instances are pinned by digest (except an image without a digest for its repository, or an older instance whose digest could not be derived yet: logged). Shared instances use the mutable line tag of their version: one instance per engine and version, never updated by the panel, and no panel operation moves that tag; `--pull missing` never updates an image already present. An update is an explicit operation (see [Version discovery and updates](#version-discovery-and-updates)).
- **Version updates**: no snapshot of the data is taken before a version change (back it up first); the mirror is trusted for the content of the images (the digest is read on it and checked after the pull, but a compromised mirror serves a consistent lie); new majors are offered without having been tested (an engine only declares its minimum version).
- **Container access**: a restricted user given access to a `svc-*` container can read its secret files. Never grant it.
- **Backups**: none.

## Testing

- **Unit tests** cover:
  - the integrity checks: edited compose, override, manifest version, private record (networks and exposure included);
  - container verification;
  - rollbacks;
  - concurrent creation under the limit;
  - the rendering and provisioning of every engine.
- **Integration tests** (`make test-integration`, Docker required) run each engine on its real images and check:
  - tenant access;
  - refused anonymous access and admin commands;
  - container hardening;
  - rotation (old password refused, data kept);
  - deletion;
  - TLS when published;
  - for RabbitMQ: cross-vhost access refused, and no listening socket outside the loopback but AMQP.
