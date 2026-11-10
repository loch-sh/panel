# Managed services

Optional module (admins only) offering databases, caches and message brokers to the hosted sites, each running from its official Docker image.

| Category | Engine | Versions | Topology | A resource is |
|---|---|---|---|---|
| Databases | PostgreSQL | 18, 17, 16, 15 | shared | a database and its user |
| | MariaDB | 11.8, 11.4, 10.11 | shared | a database and its user |
| | MySQL | 8.4, 5.6 (out of support) | shared | a database and its user |
| | MongoDB | 8.0, 7.0, 6.0 (out of support) | shared | a database and its user |
| Cache | Redis | 8.4, 8.2, 7.4 | dedicated | an instance |
| | Valkey | 9.0, 8.1, 7.2 | dedicated | an instance |
| Messaging | RabbitMQ | 4.2, 4.1, 4.0 | shared | a virtual host and its user |
| | NATS | 2.12, 2.11, 2.10 | dedicated | an instance |
| Queues | ElasticMQ (Amazon SQS-compatible queues) | 1.7 | dedicated | an instance; **no authentication** |
| | Eclipse Mosquitto (MQTT broker) | 2.0 | dedicated | an instance (one MQTT user) |
| Storage | Loch S3 (S3-compatible object storage) | 1.4 | dedicated | an instance (the tenant is its root user) |

The versions of the table are those of the built-in catalog, used until the first discovery succeeds. The panel then discovers the published versions itself, see [Versions and updates](#versions-and-updates).

## Shared and dedicated instances

- **Shared**: one instance per engine version (`svc-postgres-17`), used by every site. It starts with its first resource and is removed, with its data, when its last resource is deleted. This suits engines that are heavy and isolate their tenants natively: RabbitMQ uses about 180 MB at rest.
- **Dedicated**: one container per resource (`svc-redis-cache`), named and sized when it is created, deleted with its data. This suits light engines (about 15 MB at rest) whose internal isolation between tenants is weak.

The reasons behind this split are in [Managed services security](services-security.md#topology).

### Options of dedicated instances

| Engine | Option | Values | Default | Change later |
|---|---|---|---|---|
| Redis, Valkey | `memory` (container memory, MB; data gets half) | 128, 256, 512, 1024, 2048, 4096 | 256 | yes |
| | `policy` (when memory is full) | noeviction, allkeys-lru, allkeys-lfu, volatile-lru, volatile-lfu, volatile-ttl | noeviction | yes |
| | `persistence` | aof (journal), rdb (snapshots), none | aof | no |
| | `scripting` (Lua) | on, off | on | yes |
| Redis 8 | `modules` (JSON, search, time series…) | off, on | off | yes |
| NATS | `memory` (container memory, MB; in-memory streams get a quarter) | 128, 256, 512, 1024 | 256 | yes |
| | `storage` (JetStream disk, MB) | 1024, 5120, 10240, 51200 | 1024 | yes |
| | `payload` (largest message, MB) | 1, 8, 64 | 1 | yes |
| ElasticMQ | `memory` (container memory, MB) | 128, 256, 512, 1024 | 256 | yes |
| | `limits` (strict: same limits as Amazon SQS; relaxed: larger messages, longer delays) | strict, relaxed | strict | yes |
| Mosquitto | `memory` (container memory, MB; the broker refuses messages beyond three quarters) | 64, 128, 256, 512 | 128 | yes |
| | `persistence` (retained messages and persistent sessions on disk) | on, off | on | yes |
| | `message-size` (largest message, MB) | 1, 10, 100 | 1 | yes |
| | `queued` (messages kept per offline or slow client, QoS 1 and 2) | 1000, 10000, 100000 | 1000 | yes |
| Loch S3 | `memory` (container memory, MB) | 128, 256, 512, 1024 | 256 | yes |
| | `quota` (MB: objects, versions, uploads in progress; below the space used, writes are refused, nothing is deleted) | 1024, 5120, 10240, 51200, 102400, 512000 | 10240 | yes |
| | `encryption` (server-side encryption of every object, SSE-S3) | off, on | off | no |

**Custom sizes**: besides the offered values, memory, quota and JetStream storage accept any size within a range ("Custom" card, entered in GB; `--option memory=3G` or `quota=1.5GB` in the CLI, converted to MB; any multiple of the step in MB in the API):

| Engine | Option | Custom range | Step |
|---|---|---|---|
| Redis, Valkey | `memory` | 128 MB to 64 GB | 128 MB |
| NATS | `memory` | 128 MB to 16 GB | 128 MB |
| | `storage` | 1 GB to 1 TB | 1 GB |
| ElasticMQ | `memory` | 128 MB to 8 GB | 128 MB |
| Mosquitto | `memory` | 64 MB to 4 GB | 64 MB |
| Loch S3 | `memory` | 128 MB to 8 GB | 128 MB |
| | `quota` | 1 GB to 10 TB | 1 GB |

The memory is reserved for the container: a size larger than the host can give makes the instance fail to start or be killed under pressure.

**Changing the options** of a dedicated instance (edit button in its list, `loch services update`, `PATCH /services/instances/{instance}`, MCP `services_update_instance`) recreates its container with them: its clients are disconnected for a few seconds and the data is kept. If the instance does not start with the new options, the previous files are restored and it is started again with them. The version and the name never change; Redis and Valkey persistence is fixed at creation, because at start the server only reads the files of the persistence in use (the data would be lost). Lowering the memory below what the data uses makes Redis evict keys (or refuse writes with `noeviction`); lowering the NATS storage below what the streams use makes JetStream refuse new messages.

The panel keeps the digest of the password (never the password) in the private directory (`.loch/<project>/password.digest`), so that the credential files can be rendered again with the new options. An instance created before this file existed gets it at its next password reset: until then, changing its options is refused (`SERVICE_PASSWORD_RESET_REQUIRED`). Every update recreates the container (`--force-recreate`), because the configuration files are read at start only; successful updates are audited as `service resource updated`, rolled-back ones as `service resource update rolled back`.

## Configuration

| Variable | Description | Default |
|---|---|---|
| `SERVICES_ENABLED` | Enables the module | `false` |
| `SERVICES_DATA_DIRECTORY` | Data of the instances, outside the stacks, sites and SFTP directories (same path on the host and in the API container) | `/data/services` |
| `SERVICES_NETWORK` | Docker name of the system network `services`, shared by the instances and the sites (created `internal` if missing; not `bridge`, `host`, `none`, `default`, the Traefik network nor a `loch-net-*` name) | `loch-services` |
| `SERVICES_ENGINES` | Enabled engines, comma-separated (empty: all) | all |
| `SERVICES_<ENGINE>_PINNED_VERSIONS` | Versions of an engine that are always offered at creation, even out of support or older than the 3 latest majors, e.g. `SERVICES_MYSQL_PINNED_VERSIONS=5.6`. One variable per engine | none |
| `SERVICES_UPDATE_CHECK_INTERVAL` | Hours between two discoveries of the versions and updates (0 to 168; `0`: only on demand, see [Versions and updates](#versions-and-updates)) | `12` |
| `SERVICES_MAX_RESOURCES_PER_INSTANCE` | Resources per shared instance | `50` |
| `SERVICES_MAX_DEDICATED_INSTANCES` | Dedicated instances per engine | `20` |
| `SERVICES_ARCHIVE_ON_RETIRE` | Rename the data of a removed instance (shared or dedicated) instead of deleting it | `false` |
| `SERVICES_EXPOSE_ALLOWED_CIDRS` | Host addresses a port may be published on, comma-separated CIDRs or addresses, see [Publishing a port](#publishing-a-port) | `127.0.0.1` |
| `SERVICES_ALLOW_PUBLIC_BIND` | Required for any non-loopback address in the list above | `false` |
| `SERVICES_TLS` | `auto`: TLS required once published on a public address; `off`: never | `auto` |
| `SERVICES_ALLOW_TLS_OPTIONAL` | Lets an exposure offer TLS without requiring it (PostgreSQL, MySQL 8.4, MariaDB) | `false` |
| `SERVICES_PUBLIC_HOST` | Host name shown to clients and covered by the certificates | none |
| `SERVICES_TLS_CERT_FILE` / `SERVICES_TLS_KEY_FILE` | Certificate to use instead of self-signed ones | none |
| `SERVICES_S3_ADMIN_API_KEY` | Loch S3 admin key. Empty: one Loch S3 instance per resource (the only mode available). Set: shared Loch S3 mode, **not available yet**: the `s3` engine is disabled (error in the logs), the other engines keep working | none |

Compose files go to `HOSTING_STACKS_DIRECTORY` (see [Web hosting](hosting.md)), so the module needs that directory even when the hosting plugin is off.

Disabling an engine deletes nothing: its instances keep running, but the panel ignores them until it is enabled again.

**Removed variables.** `SERVICES_EXPOSED_PORTS`, `SERVICES_PUBLISH_ADDRESS`, `SERVICES_TLS_OPTIONAL` and `SERVICES_<ENGINE>_VERSIONS` no longer exist: the API refuses to start while one is set, and says what replaces it (ports are now chosen in the panel, see [Publishing a port](#publishing-a-port); versions are discovered, a version that must always be offered is pinned with `SERVICES_<ENGINE>_PINNED_VERSIONS`). A wrong value, an unknown engine or version, or an unreadable certificate also stops the API at startup, with every problem listed (see [Configuration](configuration.md#startup-checks)); only the installation checks (paths, mounts, Docker, `docker compose`) disable the module without stopping the server.

## Versions and updates

### Version catalog

The versions of **every engine** are discovered by the panel instead of being listed in the code:

1. **Tags** are listed on the origin registry of the engine (Docker Hub anonymously, `ghcr.io` for Loch S3: `mirror.gcr.io` cannot list tags) and kept only if they match a strict pattern declared by the engine.
2. **Availability**: each candidate image is checked (manifest `HEAD`, which gives its digest) on the registry it is pulled from, that is `mirror.gcr.io` (see the [mirror rule](development.md#conventions)). A version that is not on the mirror is not offered.
3. **Support dates** come from [endoflife.date](https://endoflife.date): the end of active support (`activeSupportUntil`) and the end of security support (`supportUntil`, which makes a version `deprecated` once passed). NATS, Mosquitto, ElasticMQ and Loch S3 are not listed there: a published version is considered supported.

**What creation offers**: the latest supported and available version of each of the **3 latest majors** (the major is the first number: MySQL 8.4 and 9.x, MariaDB 11.8 and 10.11...), plus the versions pinned with `SERVICES_<ENGINE>_PINNED_VERSIONS`, always offered, even out of support (a pinned version that is not on the mirror is not offered). MySQL 5.6 is only offered when pinned. Versions that are out of support upstream are otherwise not offered.

**Existing instances stay managed whatever their version** (any version the engine supports, from its minimum): a shared instance of a version that is no longer offered keeps accepting new resources, and a dedicated one keeps running. Only the offer for new instances changes.

**When**: one minute after startup, then every `SERVICES_UPDATE_CHECK_INTERVAL` hours (default 12). With `0`, never periodically, only on demand: "Check for updates" button, `loch services updates --check`, `POST /services/updates/check` (scope `services:write`, throttled to one check per 30 seconds). The result is cached in `DATA_DIR/services-catalog.json` (validated again when loaded). Until a first discovery succeeds, and for an engine whose discovery never succeeded, the built-in catalog of the engine is used. When a discovery fails, the previous values are kept and the engine is listed in `catalog.failed` of `GET /services/config` (details in the API logs).

**Outbound HTTPS** is required to `registry-1.docker.io`, `auth.docker.io`, `ghcr.io`, `mirror.gcr.io` and `endoflife.date`. Without it the built-in catalogs are used and no update is offered.

Newer majors are offered **without having been tested** with the panel: an engine only declares the oldest version its code supports. Create a test instance first when a new major appears.

MySQL 9 and later have no `mysql_native_password`: the legacy authentication option is only offered on MySQL 8.4 to 8.x.

### Updating a dedicated instance

Dedicated instances (Redis, Valkey, NATS, Loch S3, ElasticMQ, Mosquitto) can be updated in one operation, from the web interface (the instance list shows the available update), `loch services upgrade`, `POST /services/instances/{instance}/update` or the MCP tool `services_upgrade_instance` (image of the current version only: a version change cannot be undone, so it is never offered to an AI agent). The instance can move to:

- **a newer image of its version**: a new release of its floating tag (a new digest on the mirror) or, for the engines that use exact tags (Mosquitto, ElasticMQ, Loch S3), a newer patch tag;
- **one of at most 3 newer supported versions** (`update.versions`, newest first, pinned newer versions included).

**Never a downgrade**, neither of version nor of patch (`SERVICE_VERSION_DOWNGRADE`): data written by a newer version may not be readable by an older one. A version that is not offered as an update is refused (`INVALID_SERVICE_VERSION`).

**Shared instances are not updated** (`SERVICE_UPDATE_NOT_SUPPORTED`): they serve several sites, and a version change is a migration of every database. Create the resources on an instance of the new version and move the data.

How it runs:

1. The digest of the target tag is read on the mirror and the image is **pulled by digest** (4 minutes at most).
2. Under the instance lock, the container is verified, the options are validated again for the target version, then the instance is rendered again **pinned to the pulled digest** (reference, state, compose and override files) and recreated (`up --force-recreate`; 5 minutes at most).
3. If anything fails, the files are restored, and with them the previous pinned image, and the instance is started again on it.

Its clients are disconnected for a few seconds, the data is kept. **No backup of the data is made before a version change**: back it up first when the data matters (a newer version may convert it on first start).

**Images pinned by digest.** The compose file of a dedicated instance runs `image: <repo>:<tag>@sha256:<digest>` (native Docker: it boots without the panel, the image being already pulled), and its private reference (`instance.json`) records `image` and `digest`. No local tag is ever moved: the instances of a version share its tag, and an update (or a rolled-back one) of one of them never changes what another one runs, even at its next recreation (options, published port, networks). At creation, the digest is the one Docker recorded when it pulled the tag (pulled first when absent, as compose would); an image without a digest for its repository (built or loaded locally) is run by its tag, and a warning is logged. Instances created before images were recorded get their image at startup; those created before images were pinned are pinned at startup to the image their verified container runs, without a restart. When that digest cannot be derived (container missing or untrusted, image without a digest for its repository), the instance keeps its tag-only compose file (logged) and is pinned by its next update. A compose file rewritten without its digest while the instance is pinned (by CI, or an interrupted operation) makes the instance **modified** until the next start, which pins it again to the image its container runs. Shared instances keep the line tag of their version (see [Managed services security](services-security.md#version-discovery-and-updates)).

The response is `{updated, version, image}`; `updated` is `false` when the instance already ran the target image. Audit events: `service instance updated`, `service instance update rolled back`.

```bash
loch services updates --check              # versions and updates now (otherwise: the last periodic check)
loch services upgrade redis-cache          # latest image of its version
loch services upgrade redis-cache --version 8.4 --yes
```

## Using it

- **Legacy authentication**: for old clients, a database can be created with an older password mechanism ("Legacy authentication" switch, `loch services create --legacy-auth`, `legacyAuth` in the API): `mysql_native_password` on MySQL 8.4 (for "Authentication requires secure connection" errors), `md5` on PostgreSQL. Fixed at creation, kept by rotations; see [Managed services security](services-security.md#legacy-authentication).

- **Web interface**:
  - the menus "Databases", "Cache", "Messaging" and "Storage" list the enabled engines;
  - creation is a full page: version, size and settings, then name.
- **CLI**:

  ```bash
  loch services engines                       # engines, versions and options
  loch services instances                     # instances and their state
  loch services create shop --engine postgres --version 17
  loch services create cache --engine redis --version 8.4 --option memory=512 --option policy=allkeys-lru
  loch services ls                            # resources
  loch services update redis-cache --option memory=1024   # resize a dedicated instance
  loch services updates [--check]             # available updates (dedicated instances) and versions
  loch services upgrade redis-cache [--version 8.4] [--yes]   # update a dedicated instance
  loch services reset-password postgres-17 shop
  loch services delete redis-cache cache
  loch services retire mariadb-11-4            # remove an empty shared instance
  loch services create shop --engine postgres --version 17 --network backend   # private network(s) of a new instance
  loch services networks postgres-17 --network backend --network services       # change the networks of an instance
  loch services expose postgres-17 127.0.0.1:5417                                # publish a port
  loch services unexpose postgres-17
  ```

- **Passwords**: the password is shown **once**, at creation or rotation. A rotation closes the open connections; a dedicated instance is restarted (a few seconds of unavailability).
- **Deletion** is final.

## Object storage (Loch S3)

Each resource is a dedicated [Loch S3](https://github.com/loch-sh/s3) instance (`svc-s3-<name>`), with the tenant as its single root user.

- **Credentials** shown once: `endpoint` (`http://svc-s3-<name>:8080`), `region` (`us-east-1`; any region is accepted), `bucket`, `accessKeyId` (derived from the secret, it changes at each rotation), `secretAccessKey`, `pathStyle` (`true`: virtual-hosted URLs are not supported). The credentials window offers the Laravel `.env` lines.
- **Laravel**: `AWS_ENDPOINT`, `AWS_USE_PATH_STYLE_ENDPOINT=true`, `AWS_DEFAULT_REGION`, `AWS_BUCKET`, `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY`. **Symfony / Flysystem**: `S3Client` with `endpoint`, `use_path_style_endpoint: true`, `region` and `credentials`.
- **Buckets**: a bucket named like the resource is created at start (`S3_INIT_BUCKETS`); the tenant, root of its instance, may create others (CreateBucket; a `LocationConstraint` body is accepted and ignored).
- **Quota**: once reached, every write is refused with `QuotaExceeded` (403), even of 0 bytes; deletions still work. A refusal during an upload may look like a dropped connection to the client. No per-object size limit besides the quota; user metadata 2 KB, keys 1024 bytes.
- **Integrity**: trailing checksums (aws-chunked, the default of recent AWS SDKs) and `x-amz-content-sha256` are accepted but not verified by Loch S3.
- **Health**: `GET /_loch/health` (unauthenticated, never logged, no figures); the usage figures are on `GET /_loch/usage`, signed by the tenant (`curl --aws-sigv4 "aws:amz:us-east-1:s3" -u KEY:SECRET http://svc-s3-<name>:8080/_loch/usage`).
- **Rotation** (`loch services reset-password s3-<name> <name>`) changes both the access key and the secret, then restarts the instance; presigned URLs signed with the old key stop working.
- **Limits**: path-style URLs only; presigned URLs use the internal address, so they do not work from a browser; no TLS in Loch S3 (publishing on a public address requires `SERVICES_TLS=off`).
- **Image**: `ghcr.io/loch-sh/s3`, our own image, not on Docker Hub: the explicit exception to the `mirror.gcr.io` rule.

## Queues (ElasticMQ, Mosquitto)

- **ElasticMQ** (`svc-elasticmq-<name>`, port 9324): an Amazon SQS-compatible API. Credentials shown: `endpoint`, `region` (`us-east-1`), `queueUrlPrefix`. **It has no authentication**: any credentials are accepted (SDKs need some, e.g. `x`), and every site on a network of the instance can use, purge or delete its queues. For that reason it is refused on the shared `services` network and lives on a [private network](networks.md) shared only with the sites allowed to use it. The interface and the CLI warn about it; there is no password to reset. Do not put sensitive data in it. Queues and messages are persisted in the data directory.
- **Eclipse Mosquitto** (`svc-mosquitto-<name>`, port 1883): an MQTT broker with one user, no anonymous access. URI `mqtt://<user>:<password>@svc-mosquitto-<name>:1883` (`mqtts://` once published with TLS). Topics `$SYS/...` are not reachable with `#`.

NATS stays in "Messaging" with RabbitMQ: both are general brokers (publish/subscribe, routing), while "Queues" holds the dedicated queue servers.

## Networks of an instance

Every instance joins one or more [private networks](networks.md), and only the sites on one of them can reach it.

- **Default**: an instance created without a network joins the system network `services` (`SERVICES_NETWORK`), shared with every site that has it.
- **Choice at creation**: `networks` in `POST /services/resources`, the network selector of the creation page, `--network` in the CLI (repeatable). For a shared engine only the first resource of a version creates the instance and therefore chooses its networks; later resources reuse the instance as it is. A dedicated instance always chooses.
- **Engines without authentication** (ElasticMQ, `unauthenticated` in `GET /services/config`) are **refused on `services`** (`INVALID_SERVICE_NETWORK`) and must be given a private network: put it on a network shared only with the sites allowed to use it. They are never published beyond the loopback.
- **Change later**: `PUT /services/instances/{instance}/networks`, `loch services networks <instance> --network ...`, or attach/detach from the network page. The container is recreated (a few seconds of unavailability, data kept); an instance keeps at least one network (`SERVICE_LAST_NETWORK`). See [Private networks](networks.md#attaching-and-detaching-a-member).
- **Recorded** in the private reference of the instance (`instance.json`), which is also written for shared instances now. The compose file lists the networks as `external` and its header gives the `docker network create` commands to boot without the panel.

**Upgrade note**: the manifest version moved to 2, so instances created by an earlier development build are shown "Edited by hand" and must be purged and created again (there is no migration: the module was not in production).

## Connecting a site

A hosted site reaches the instances by joining the network of the instance, in its `networks` setting (see [Web hosting](hosting.md#networks)): it then uses the internal address shown with the credentials (`svc-postgres-17:5432`, `svc-redis-cache:6379`...). The `services` network is preselected for new sites when the module is enabled. A site on no common network with the instance cannot reach it.

## Publishing a port

A port is published **per instance, from the panel** (instance page "Exposure", `loch services expose`, `PUT /services/instances/{instance}/exposure`), within bounds that only the server administrator sets in the environment. The scope `services:expose` is required for API keys; it is **not** implied by `services:write`, and no MCP tool publishes a port. Closing is `DELETE /services/instances/{instance}/exposure` (`loch services unexpose`). Applying a change recreates the container (a few seconds of unavailability).

```bash
loch services expose postgres-17 203.0.113.10:5417
loch services expose mariadb-11-8 203.0.113.10:3318 --tls-optional
loch services unexpose postgres-17
```

**Policy (environment only)**:

| Rule | Source |
|---|---|
| The address must be inside one of `SERVICES_EXPOSE_ALLOWED_CIDRS` (default `127.0.0.1`). `0.0.0.0` only if listed alone, never through a range. These are **host addresses the port listens on**, not the clients allowed: Docker cannot filter the clients of a published port, use the `DOCKER-USER` iptables chain for that | `SERVICES_EXPOSE_ALLOWED_CIDRS` |
| A non-loopback address needs `SERVICES_ALLOW_PUBLIC_BIND=true`. Docker bypasses ufw and firewalld | `SERVICES_ALLOW_PUBLIC_BIND` |
| Port from 1024 to 32767, except 2375, 2376, 2538, 8080 and the API and SFTP ports; two instances cannot share a port, and a port used by another container is refused (`SERVICE_PORT_IN_USE`) | built in |
| Out-of-support versions (MySQL 5.6, MongoDB 6.0), engines without authentication (ElasticMQ) and engines that cannot require TLS (MongoDB, while `SERVICES_TLS=auto`): loopback only | built in |
| With `SERVICES_TLS=auto`, a non-loopback address requires TLS from **every** client of the instance, the sites included, on the same port. Clients get the certificate fingerprint with the credentials | `SERVICES_TLS` |
| "TLS optional" (TLS offered, not required) only if allowed, on a non-loopback address, for PostgreSQL, MySQL 8.4 and MariaDB. Plain clients then send their password and data in clear: prefer it to `SERVICES_TLS=off`, which disables TLS everywhere | `SERVICES_ALLOW_TLS_OPTIONAL` |

`GET /services/config` returns the policy (`exposure`: allowed CIDRs, port range, reserved ports, `allowTlsOptional`, `tlsMode`) so that clients can show the rules; the server enforces them (`INVALID_SERVICE_EXPOSURE`, with the faulty `field`).

The exposure is recorded in the private reference of the instance and applied by the same fail-closed path as at startup (then once a day, certificate renewal included): a change that cannot be applied keeps the previous port, or stops the instance when the port had to be closed or narrowed (`SERVICE_EXPOSURE_FAILED`, details in the API logs). Every change is audited (`service exposure changed`).

The policy is checked again at every application, not only when the exposure is chosen: after tightening it (an address removed from `SERVICES_EXPOSE_ALLOWED_CIDRS`, `SERVICES_ALLOW_PUBLIC_BIND` or `SERVICES_ALLOW_TLS_OPTIONAL` turned off, a version gone out of support), the next application (startup, then daily) closes the exposures it no longer allows (audit `service exposure revoked by policy`). `compose.override.yaml` is only trusted when it is exactly what the panel renders from the recorded exposure: an override rewritten by CI, even consistent with itself, makes the instance "not ready" until the recorded exposure is applied again.

## Troubleshooting

| Symptom | Cause |
|---|---|
| The menus show "could not start" (`SERVICES_MISCONFIGURED`) | An installation check failed at startup (paths, mounts, Docker, `docker compose`, the services network): the reason is in the API logs. Invalid variables do not get here: they stop the API |
| An instance is "Edited by hand" | Its files differ from what the panel writes, or from its private record. The panel no longer runs it: restore the files, or delete the instance with its data (Delete button, `loch services purge <instance>`, `DELETE /services/instances/{instance}?purge=true`; the name must be typed in the interface) |
| An instance is "Unverified" or "Not running" | Its container does not match what the panel started, or is stopped: see the API logs |
| No update or new version is ever offered, `catalog.failed` lists engines | The registries or endoflife.date cannot be reached (outbound HTTPS, see [Versions and updates](#versions-and-updates)): the built-in or previous catalog is used; details in the API logs |
| "Change failed" next to a published port | The last exposure could not be applied: see the API logs, then change or close it |
| An instance is "Unverified" with a network message | A network of the instance is missing or is not the one the panel created: see [Private networks](networks.md#network-states) |

The security measures are detailed in [Managed services security](services-security.md), the routes in the [API reference](api.md#managed-services).
