# API reference

The API listens on port `2538`. Browsers authenticate with a session cookie (OIDC), other clients with an API key in the `X-API-Key` header.

Responses are JSON. Errors have the form `{"code": "ERROR_CODE", "error": "message"}`; the code is stable and translated by the web interface. The rate limit (120 requests per minute per IP, the IP being read from `X-Forwarded-For` only behind a proxy listed in `TRUSTED_PROXIES`) answers `429` in plain text.

Write requests coming from a browser must carry the `Origin` of `FRONT_URL`, otherwise they get `403 FORBIDDEN_ORIGIN`. Clients without a browser (the CLI, scripts) are not concerned.

## Public

| Method | Route | Description |
|---|---|---|
| GET | `/_health` | Health check (includes the Docker status) |
| GET | `/openid-connect/login` | Start an OIDC login |
| GET | `/openid-connect/redirect` | OAuth callback |
| GET | `/openid-connect/logout` | Logout |

## Containers

| Method | Route | Description |
|---|---|---|
| GET | `/users/me` | Current user |
| GET | `/docker` | Containers (filtered by permissions) |
| GET | `/docker/{id}` | Container details |
| POST | `/docker/{id}/{action}` | `start`, `stop`, `kill`, `restart`, `delete` |
| POST | `/stacks/{name}/{action}` | `start`, `stop`, `restart` every accessible container of a compose stack, in `depends_on` order |
| GET | `/logs/{id}` | Logs (text) |
| GET | `/logs/{id}/json` | Logs (JSON) |
| GET | `/logs/{id}/file?path=&tail=` | Last lines of a file inside the container |
| GET | `/docker/{id}/cp?path=` | Download a file or directory (tar stream) |
| PUT | `/docker/{id}/cp?path=` | Upload a file or directory (tar stream) |
| WS | `/ws` | Shell, live logs, live stats, port proxy (see [WebSocket](#websocket)) |

## Domains

| Method | Route | Access | Description |
|---|---|---|---|
| GET | `/domains` | admin | Domains routed by Traefik |

## Scheduled tasks

| Method | Route | Description |
|---|---|---|
| GET | `/cron` | Tasks |
| POST | `/cron` | Create |
| GET | `/cron/{id}` | Details |
| PUT / PATCH | `/cron/{id}` | Update / partial update |
| DELETE | `/cron/{id}` | Delete |
| POST | `/cron/{id}/run` | Run now (202) |

Admins see every task. Restricted users see, change and delete every HTTP task (whoever created it) and the Docker tasks of the containers they are allowed to use; they can only create Docker tasks on those containers.

Task types: `docker_action`, `docker_exec`, `http` and `function_invoke`. Every task also carries `lastSkipped` (the last time a run was skipped because the previous one was still running; a run never overlaps the previous one of the same task) and `disabledReason` (set when the panel disabled the task itself; cleared when it is enabled again).

### `function_invoke` tasks

Invoke a [function](functions.md) on a schedule, through the functions module (`docker exec` into the verified container, like `POST /functions/{name}/invoke`; never over HTTP nor a network bridge):

```json
{
  "name": "nightly thumbnails",
  "schedule": "0 3 * * *",
  "type": "function_invoke",
  "enabled": true,
  "functionInvoke": { "function": "thumbs", "event": "{\"bucket\":\"media\"}" }
}
```

- `functionInvoke.function`: an existing function. `functionInvoke.event`: the JSON **text** of the event, one JSON value of any type, at most 64 KiB (the event is stored in `cron.json`, rewritten at every run; with `CRON_MAX_JOBS` this bounds the file); empty or missing = `{}` (stored and returned as `"{}"`).
- A task keeps the configuration block of its type only: changing the type drops the previous block (and `disabledReason`).
- **Access**: admins only (restricted profiles neither see nor create these tasks). Creating, updating (`PUT`, `PATCH`, also to turn such a task into another type or the other way round) and running one (`POST /cron/{id}/run`) needs `functions:invoke` on top of `cron` for an API key (`INSUFFICIENT_SCOPE` 403). Deleting needs `cron` only.
- **Checks**: the module enabled (`FUNCTIONS_DISABLED` 404, also for a manual run) and the function existing (`FUNCTION_NOT_FOUND` 404, `field: functionInvoke.function`), both skipped when the task stays (or is turned) disabled on the function it already had: such a task can always be disabled, renamed or rescheduled, even with the module off or the function gone, the event (`INVALID_REQUEST_BODY` 400), at most 5 tasks per function (`FUNCTION_CRON_LIMIT_REACHED` 409; the panel runs at most 2 invocations of a function at a time). The minimum period is 1 minute, as for every task (`CRON_FREQUENCY_TOO_HIGH`).
- **Run**: a missing or stopped function, a busy one (`FUNCTION_INVOKE_BUSY`), a function error (`functionError`, e.g. `Unhandled`) or a status ≥ 400 make the run fail (`lastStatus: "error"`, `lastError` with the reason and the code). `lastOutput` is a JSON text holding the outline of the result only, **never the payload nor the logs** (untrusted data written by the function): `{"statusCode":200,"functionError":"Unhandled","durationMs":12,"payloadBytes":34}` (`functionError` only when set; empty after an invocation that did not complete).
- **Deleted function**: its tasks are kept but disabled, with `disabledReason: "FUNCTION_DELETED"` (also after a purge, and at startup for an enabled task whose function no longer exists). Point them at another function to enable them again, or delete them.
- **Module disabled**: the tasks are kept as they are but not scheduled (one warning in the logs at startup); they are scheduled again when the API restarts with the module.
- **Audit**: the usual `cron job *` entries (a task disabled by a deletion writes `cron job updated` with `reason: function deleted`); the invocations themselves are in the aggregated `function invoked` entry, whose `user` is `cron:<task id>`.

`GET /cron/{id}` of such a task:

```json
{
  "id": "5f0c…",
  "name": "nightly thumbnails",
  "schedule": "0 3 * * *",
  "type": "function_invoke",
  "enabled": true,
  "once": false,
  "functionInvoke": { "function": "thumbs", "event": "{\"bucket\":\"media\"}" },
  "lastRun": "2026-11-06T03:00:00Z",
  "lastStatus": "success",
  "lastOutput": "{\"statusCode\":200,\"durationMs\":12,\"payloadBytes\":34}",
  "createdAt": "2026-11-05T10:00:00Z",
  "updatedAt": "2026-11-05T10:00:00Z"
}
```

`lastError`, `lastSkipped` and `disabledReason` appear when set.

## Users and API keys

| Method | Route | Access | Description |
|---|---|---|---|
| GET | `/users/me/api-keys` | every user | Own keys (never the token) |
| POST | `/users/me/api-keys` | every user | Create a key (token shown once) |
| DELETE | `/users/me/api-keys/{id}` | every user | Revoke a key |
| GET | `/users` | admin | Users |
| PATCH | `/users/{email}/switch` | admin | Toggle admin / restricted |
| PATCH | `/users/{email}/restrict` | admin | Make restricted |
| PATCH | `/users/{email}` | admin | Update the container permissions |
| DELETE | `/users/{email}` | admin | Delete (and revoke the user's keys) |

## SFTP users

Admin only, scopes `sftp:read` / `sftp:write`. See [SFTP users](sftp.md).

| Method | Route | Description |
|---|---|---|
| GET | `/sftp/config` | Plugin status and connection details |
| GET | `/sftp/sites` | Sites |
| GET | `/sftp/users` | Users |
| POST | `/sftp/users` | Create |
| PUT / DELETE | `/sftp/users/{username}` | Update / delete |
| POST | `/sftp/users/{username}/password` | New password |
| POST | `/sftp/users/{username}/status` | Enable / disable |

## Web hosting

Admin only, scopes `hosting:read` / `hosting:write`. See [Web hosting](hosting.md).

| Method | Route | Description |
|---|---|---|
| GET | `/hosting/config` | Plugin status, templates, PHP versions, defaults, limits, `defaultNetworks` (networks to preselect for a new site: `["services"]` when the services module is enabled) |
| GET | `/hosting/sites` | Site directories and the hosting using each |
| GET | `/hosting/php-modules?template=&version=` | PHP modules of an image |
| GET | `/hosting/routing-template` | Routing template |
| GET | `/hosting/stacks` | Sites with their status |
| GET | `/hosting/stacks/check-name?name=` | Name availability |
| POST | `/hosting/stacks` | Create and deploy (202, asynchronous). The settings carry `networks: string[]` (registry names, sorted by the server), which replaces the former `servicesNetwork` boolean; same field in `PUT` and in the settings returned by `GET /hosting/stacks/{name}`. `INVALID_HOSTING_NETWORK` 400 (field `networks`) replaces `INVALID_SERVICES_NETWORK` |
| GET | `/hosting/stacks/{name}` | Settings, containers, last operation |
| PUT | `/hosting/stacks/{name}[?force=true]` | Update and redeploy, rolled back on failure (202) |
| POST | `/hosting/stacks/{name}/deploy[?pull=true][&force=true]` | Redeploy (202); `force` overwrites changes made outside the panel |
| DELETE | `/hosting/stacks/{name}[?purgeData=true]` | Delete; site data kept unless `purgeData` (202) |

## Managed services

Admin only, scopes `services:read` / `services:write` (`services:expose` and `networks:write` where noted). See [Managed services](services.md).

| Method | Route | Description |
|---|---|---|
| GET | `/services/config` | Module status, engines (kind, lifecycle, versions with `legacyAuth`, `tls` (can require TLS) and `tlsOptional` (can offer it without requiring it), options with their `custom` size range `{min, max, step}` in MB, `unauthenticated`), the system network name, the exposure policy `exposure: {allowedCidrs, minPort, maxPort, reservedPorts, allowTlsOptional, tlsMode}` and the state of the version discovery `catalog: {checkedAt?, failed?: string[], throttled?}` (`checkedAt` absent: never discovered, the built-in catalogs are used; `failed`: engines whose last discovery failed). Versions carry `supportUntil` and `activeSupportUntil` (`YYYY-MM-DD`, when known), `deprecated` and `pinned`. `exposures` and the top-level `tlsMode` are gone |
| GET | `/services/instances` | Instances with their state, `networks: string[]`, `unauthenticated?`, `exposure?: {address, port, tls, tlsOptional?, fingerprint?}` (in effect) and `exposureError?`, `supportUntil?` and `activeSupportUntil?` (end of support of its version) and, for a dedicated instance, `update?: {image, latestImage?, versions?}`: `image` is true when a newer image of the same version exists (`latestImage`), `versions` are the newer versions it can move to (at most 3, newest first) |
| GET | `/services/instances/{instance}/resources` | Resources of an instance |
| POST | `/services/resources` | Create `{engine, version, name, options, legacyAuth, networks?}`; `networks` are the networks of the instance when this request creates it (empty: `services`; refused on `services` for an engine without authentication: `INVALID_SERVICE_NETWORK`); returns the credentials once. `legacyAuth` (shared engines, versions with `legacyAuth: true` in the configuration): MySQL 8.4 mysql_native_password, PostgreSQL md5. A new dedicated instance is pinned to the digest of its image (`SERVICE_IMAGE_PULL_FAILED` 502 when the image is absent and cannot be pulled) |
| POST | `/services/instances/{instance}/resources/{name}/password` | New password, returned once |
| DELETE | `/services/instances/{instance}/resources/{name}` | Delete a resource (a dedicated instance is deleted with it) |
| PATCH | `/services/instances/{instance}` | Change the mutable options of a dedicated instance `{options}`, which is recreated; returns `{options}` (`SERVICE_OPTION_IMMUTABLE` 400 for a fixed option) |
| PUT | `/services/instances/{instance}/networks` | Replace the networks `{networks}` (scope `networks:write`); the container is recreated; returns `{networks}`. `INVALID_SERVICE_NETWORK` 400, `SERVICE_LAST_NETWORK` 400 (an instance keeps one) |
| PUT | `/services/instances/{instance}/exposure` | Publish `{address, port, tlsOptional?}` (scope `services:expose`); applied at once (container recreated); returns the instance. `INVALID_SERVICE_EXPOSURE` 400 (with `field`), `SERVICE_PORT_IN_USE` 409, `SERVICE_EXPOSURE_FAILED` 502 |
| DELETE | `/services/instances/{instance}/exposure` | Close the port (scope `services:expose`); returns the instance |
| POST | `/services/instances/{instance}/update` | Update a dedicated instance `{version?}` to the latest image of its version, or to a newer version of `update.versions` (the image is pulled by digest and the instance pinned to it, `repo:tag@digest`, no local tag is moved; the instance is recreated, its data kept; slow: pull then restart, up to about 10 minutes). Returns `{updated, version, image}`. `SERVICE_UPDATE_NOT_SUPPORTED` 400 (shared instance), `SERVICE_VERSION_DOWNGRADE` 400, `INVALID_SERVICE_VERSION` 400 (not offered as an update), `SERVICE_IMAGE_PULL_FAILED` 502, `SERVICE_INSTANCE_START_FAILED` (previous image restored), `SERVICE_RELEASES_UNAVAILABLE` 503 |
| POST | `/services/updates/check` | Discover the versions and updates now (registries, endoflife.date); scope `services:write`, throttled to one check per 30 seconds (`throttled: true`, the last result is returned). Returns the `catalog` state. `SERVICE_RELEASES_UNAVAILABLE` 503 when the discovery is not configured |
| DELETE | `/services/instances/{instance}` | Remove an empty shared instance; with `?purge=true`, delete an instance edited by hand with all its data, without running its files (`SERVICE_INSTANCE_NOT_MODIFIED` 409 otherwise) |

## Functions (work in progress)

Admin only. Scopes: `functions:read` (reads), `functions:write` (resources, start / stop / restart, creation and deletion), `functions:deploy` (code, deployments, secrets, invoke credentials, and everything that decides where the code can send its secrets: variables, handler, networks, egress), `functions:invoke` (invocations; implied by none of the others, only by `*`). A creation with `env`, `networks`, `egress`, `sigv4` or `mcpInvocable`, and a `PATCH` with `env`, `handler` or `mcpInvocable`, need both `functions:write` and `functions:deploy`. Errors of `POST /functions/{name}/code` and `/invoke` close the connection (their body may be left unread). See [Functions](functions.md). Bodies are strict (`INVALID_REQUEST_BODY` 400). `mcpInvocable` (boolean, `false` by default, in `GET /functions` and `GET /functions/{name}`) opts a function in to invocation by the loch MCP tool: a **guard-rail checked by the MCP client, not an access control** (the API cannot tell an MCP client from another one, and `/invoke` answers every `functions:invoke` holder); see [Functions security](functions-security.md#mcp-opt-in-mcpinvocable). No function can run until a trusted runtime binary is installed: the panel downloads it by itself (`runtime` and `runtimeReady` in the configuration; `FUNCTION_RUNTIME_UNAVAILABLE` meanwhile).

| Method | Route | Description |
|---|---|---|
| GET | `/functions/config` | `{enabled, error?, config?: {arch, runtimes: [{id, versions, custom?, defaultHandler?}], runtimeReady, runtime: {state: idle|installing|installed|unavailable|untrusted, version?}, max, count, minMemoryMB, maxMemoryMB, maxTotalMemoryMB, maxTotalCpus, usedMemoryMB, usedCpus, maxCodeBytes, deploymentsKept, defaults, maxTimeoutSeconds, maxEventBytes}}`; `error` is `FUNCTIONS_MISCONFIGURED` when the module is enabled but its installation check failed (details in the API logs) |
| GET | `/functions` | `[{name, runtime, runtimeVersion, handler, memoryMB, cpus, timeoutSeconds, tmpSizeMB, state, health?, deployment?, networks, egress, mcpInvocable, createdAt}]`. `state`: `not_deployed`, `running`, `stopped`, `missing`, `modified` (compose project edited outside the panel: never run, purge only), `unverified` (a container runs but is not the reference's), `invalid` (unreadable reference: purge only) |
| POST | `/functions` | Create `{name, runtime, runtimeVersion, handler, memoryMB?, cpus?, timeoutSeconds?, tmpSizeMB?, env?: [{name, value}], networks?, egress?, sigv4?, mcpInvocable?}` (201): the image is resolved and pulled by digest, the network `fn-<name>` created and joined, nothing runs until code is deployed. `runtime: "provided", runtimeVersion: "al2023"` is the custom runtime (Go, Rust…: a `bootstrap` executable), whose `handler` is optional (default `bootstrap`). Returns `{function, credentials: {token, accessKeyId?, secretAccessKey?, endpoint, region}}`, **shown once** (`Cache-Control: no-store`) |
| GET | `/functions/{name}` | The summary plus `arch`, `image`, `env`, `secrets` (names only), `deployments`, `endpoint`, `sigv4`, `problem?` (why it is `unverified`); the container is fully verified |
| PATCH | `/functions/{name}` | Change `{memoryMB?, cpus?, timeoutSeconds?, tmpSizeMB?, handler?, env?, mcpInvocable?}` (`env` replaces every variable; an empty `handler` resets a custom runtime's (`provided`) to `bootstrap`, and is refused on the others; `env`, `handler` and `mcpInvocable` also need `functions:deploy`); a running function is recreated, except for `mcpInvocable` alone (written to the reference only). A change that does not raise the limits is never refused by the global budget |
| DELETE | `/functions/{name}` | Delete: containers, compose project, every deployment, secrets, credentials, reference and the network `fn-<name>` (kept when other members are still attached: `networkKept`). Returns `{containers, networkKept?}`. With `?purge=true`: a function modified outside the panel (or with an unreadable reference), removed without running any of its files (`FUNCTION_NOT_MODIFIED` 409 otherwise; a plain delete of a modified one is `FUNCTION_MODIFIED` 409) |
| POST | `/functions/{name}/code` | Deploy a zip: `application/zip` / `application/octet-stream` body, or `multipart/form-data` with a `code` file part; streamed to disk, bounded by `FUNCTIONS_MAX_CODE_SIZE` (413 `FUNCTION_CODE_TOO_LARGE`, 415 for another type). An upload that makes no progress for 15 s, or averages under 32 KiB/s after 30 s, is cut (408 `FUNCTION_CODE_TIMEOUT`, connection closed). The new deployment is activated (201, the deployment, with `warnings?: ["FUNCTION_BOOTSTRAP_DYNAMIC"]` on the custom runtime; unknown codes are to be ignored); on the custom runtime the `bootstrap` is checked first (`FUNCTION_BOOTSTRAP_*`, see [Functions](functions.md#custom-runtime-go-rust-any-compiled-language)); the oldest kept ones beyond `FUNCTIONS_DEPLOYMENTS_KEPT` are removed |
| GET | `/functions/{name}/deployments` | `[{id, treeHash, zipSha256, zipSize, files, bytes, active, warnings?}]`, newest first (`warnings`: deploy-time warning codes, `FUNCTION_BOOTSTRAP_DYNAMIC`; ignore unknown ones) |
| POST | `/functions/{name}/deployments/{id}/activate` | Activate a kept deployment (rollback when older), its tree verified first (204) |
| PUT | `/functions/{name}/secrets/{key}` | Set a secret `{value}` (204): one line of UTF-8, at most 4096 bytes; the value is never returned, logged nor audited; the container is recreated |
| DELETE | `/functions/{name}/secrets/{key}` | Remove a secret (204) |
| POST | `/functions/{name}/credentials/rotate` | New invoke credentials, returned once (`no-store`). Optional body `{sigv4: true\|false}` enables or disables the SigV4 key pair (off by default) |
| POST | `/functions/{name}/invoke` | Invoke with the body as the event: one JSON value (`INVALID_FUNCTION_EVENT` 400 otherwise, empty included), at most 6 MiB (413 `FUNCTION_EVENT_TOO_LARGE`), received within 30 s of the first read (408 `FUNCTION_EVENT_TIMEOUT`); the function and a slot are checked before the body is read. Always 200 with the fixed envelope `{statusCode, functionError?, requestId?, durationMs, payload? \| payloadBase64?, logTail}` (`application/json`, `nosniff`, `Content-Security-Policy: default-src 'none'; sandbox`, `no-store`); `payload` is the function's response as text when it is UTF-8, `payloadBase64` otherwise; `logTail` is the end of its logs (4 KiB). Never a header or content type of the function. Untrusted data: render it as text |
| POST | `/functions/{name}/{action}` | `start`, `stop`, `restart` (204) |
| PUT | `/functions/{name}/networks` | Replace the networks `{networks}` (at least one, never `services`; scope `functions:deploy`); the container is recreated, removed networks cut first; returns `{networks}` |
| PUT | `/functions/{name}/egress` | Outbound access `{enabled}` (scope `functions:deploy`); returns `{egress}` |

Errors: `FUNCTIONS_DISABLED` 404, `FUNCTION_NOT_FOUND` 404, `INVALID_FUNCTION_NAME` / `_RUNTIME` / `_HANDLER` / `_ENV` / `_SECRET` / `_LIMITS` / `_NETWORK` / `_DEPLOYMENT` / `_ACTION` 400, `FUNCTION_ENV_RESERVED` 400, `INVALID_FUNCTION_EVENT` 400, `FUNCTION_EVENT_TIMEOUT` 408, `FUNCTION_CODE_INVALID` 400, `FUNCTION_BOOTSTRAP_MISSING` / `FUNCTION_BOOTSTRAP_ARCH` / `FUNCTION_BOOTSTRAP_INVALID` 400 (custom runtime), `FUNCTION_CODE_TOO_LARGE` 413, `FUNCTION_CODE_TIMEOUT` 408, `FUNCTION_UPLOADS_BUSY` 429, `FUNCTION_NO_SPACE` 507, `FUNCTION_EXISTS` / `FUNCTION_NAME_CONFLICT` / `FUNCTION_LIMIT_REACHED` / `FUNCTION_BUDGET_EXCEEDED` / `FUNCTION_OPERATION_IN_PROGRESS` / `FUNCTION_MODIFIED` / `FUNCTION_NOT_MODIFIED` / `FUNCTION_NOT_DEPLOYED` / `FUNCTION_NOT_RUNNING` / `FUNCTION_VERIFICATION_FAILED` / `FUNCTION_ARTIFACT_MODIFIED` / `FUNCTION_DEPLOYMENT_ACTIVE` 409, `FUNCTION_DEPLOYMENT_NOT_FOUND` / `FUNCTION_SECRET_NOT_FOUND` 404, `FUNCTION_RUNTIME_UNTRUSTED` / `FUNCTION_RUNTIME_UNAVAILABLE` 503 (the runtime binary is being installed or could not be: retry later, details in the logs), `FUNCTION_START_FAILED` / `FUNCTION_IMAGE_PULL_FAILED` / `FUNCTION_INVOKE_FAILED` / `FUNCTION_DOCKER_ERROR` 502, `FUNCTION_OUTCOME_UNKNOWN` / `FUNCTION_INVOKE_TIMEOUT` 504, `FUNCTION_INVOKE_BUSY` 429, `FUNCTION_EVENT_TOO_LARGE` 413, `FUNCTION_STORAGE_ERROR` / `FUNCTION_REFERENCE_INVALID` 500 (generic message: details in the logs).

Function containers are protected in the container routes: `GET /docker/{id}` masks their environment values (`***`); shell, `cp`, file tail, `loch proxy`, cron `docker_exec` and `docker_action`, `POST /docker/{id}/{action}` and `POST /stacks/fn-*/{action}` are refused with `FUNCTION_CONTAINER_PROTECTED` 403 (use `POST /functions/{name}/{action}`); their stdout logs stay readable by admins. They are never reachable by a restricted profile (`PATCH /users/{email}` refuses an `fn-*` permission, stored ones are dropped at load). A container counts as a function's when it has the `sh.loch.function` label or its name or compose project starts with `fn-`: an unrelated container named so is protected too (fails safe).

## Private networks

Admin only, scopes `networks:read` / `networks:write`. See [Private networks](networks.md). Bodies are strict: unknown fields are refused (`INVALID_REQUEST_BODY` 400).

| Method | Route | Description |
|---|---|---|
| GET | `/networks` | `[{name, dockerName, id, subnet, gateway?, description?, system?, createdAt, createdBy?, state, problem?, members}]`; the system network `services` first. `state` is `ok`, `missing` or `compromised`; `problem` is a stable code (`replaced`, `not_bridge`, `not_internal`, `unexpected_options`, `missing_label`, `unexpected_label`, `compose_project`, `unexpected_subnet`, `inspect_failed`); `members` is the number of containers on the Docker network |
| GET | `/networks/config` | Creation rules `{max, count, reservedRanges, privateRanges, minPrefix, maxPrefix}` |
| GET | `/networks/docker` | Docker networks the panel did not create (read-only): `[{id, name, driver, scope, internal, subnets, gateways, origin: docker\|compose\|other, composeProject?, createdAt, containers, reservedOverlap?}]` |
| GET | `/networks/docker/{id}` | One of them by full ID, plus `members` (same shape as below, `expected` always false); `NETWORK_NOT_FOUND` for a panel network |
| GET | `/networks/{name}` | The same fields, plus `internal` and `members`, **an array** here: `{kind, name, container?, containerId?, state?, ipv4?, mac?, expected, present}`. `kind`: `site`, `instance`, `function`, `panel`, `unmanaged` |
| POST | `/networks` | Create `{name, description?, subnet?}` (201). An empty subnet lets Docker allocate one; otherwise a private IPv4 network address, `/16` to `/28` |
| PATCH | `/networks/{name}` | Change `{description}` (the subnet cannot change) |
| DELETE | `/networks/{name}` | Delete (204) |
| POST | `/networks/{name}/members` | Attach `{kind: "site" \| "instance" \| "function", name}` (kind `function`: also `functions:deploy`, same for the detach); returns `{operation?}`: a site is redeployed asynchronously (`operation` is the hosting operation id), an instance or a function is recreated synchronously. The member's container is recreated |
| DELETE | `/networks/{name}/members/{kind}/{member}` | Detach, same response |
| POST | `/networks/{name}/disconnect` | Disconnect a container the panel does not manage `{containerId}` (204) |

Errors: `NETWORK_NOT_FOUND` 404, `INVALID_NETWORK_NAME` 400, `NETWORK_NAME_TAKEN` 409, `INVALID_SUBNET` 400, `NETWORK_IN_USE` 409, `NETWORK_LIMIT_REACHED` 409, `NETWORK_SYSTEM` 409 (the `services` network cannot be deleted), `NETWORK_COMPROMISED` 409, `NETWORK_MEMBER_NOT_FOUND` 404, `NETWORK_MEMBER_MANAGED` 409, `INVALID_NETWORK_MEMBER_KIND` 400, `NETWORK_DOCKER_ERROR` 502, `NETWORK_STORAGE_ERROR` 500, `NETWORK_ALREADY_ATTACHED` 409, `NETWORK_NOT_ATTACHED` 409, `NETWORK_OPERATION_IN_PROGRESS` 409 (another network operation ran for more than 5 minutes). Attaching or detaching can also return the errors of the member: the services codes (`SERVICE_LAST_NETWORK`, `SERVICE_INSTANCE_MODIFIED`...) or the hosting ones.

## Audit trail

Admin only, scope `audit:read`. See [Audit trail](audit.md).

| Method | Route | Description |
|---|---|---|
| GET | `/audit?from=&to=&user=&event=&exclude=&level=&q=&before=&limit=` | Entries, newest first. `from` / `to` are RFC 3339 dates (`to` exclusive); `event`, `exclude` and `level` may repeat; `limit` up to 500 |

One request reads at most 64 MB of the log: a page can be shorter than `limit`, even empty, while older entries remain. Continue as long as the response has a `next` cursor, passed as `before`.

## API key scopes

A key can be limited to some permissions. JWT sessions (browser) always have every permission of their user, and legacy keys without scopes keep full access.

| Scope | Grants |
|---|---|
| `*` | Everything (cannot be combined with other scopes) |
| `containers:read` | `GET /docker`, `GET /docker/{id}` |
| `containers:logs` | `GET /logs/**`, WebSocket `logsStart` |
| `containers:actions` | `POST /docker/{id}/{action}`, `POST /stacks/{name}/{action}` |
| `containers:shell` | WebSocket `attach` |
| `containers:stats` | WebSocket `statsStart` |
| `containers:proxy` | WebSocket `proxyOpen` / `streamOpen` |
| `containers:cp` | `GET` / `PUT /docker/{id}/cp` |
| `cron` | `/cron/**` |
| `sftp:read` / `sftp:write` | SFTP reads / changes |
| `hosting:read` / `hosting:write` | Hosting reads / changes |
| `services:read` / `services:write` | Managed services reads / changes |
| `services:expose` | Publish and close the port of an instance. Not implied by `services:write` |
| `networks:read` / `networks:write` | Private networks reads / changes, including the networks of an instance (`PUT /services/instances/{instance}/networks`) |
| `functions:read` / `functions:write` | Functions reads / changes of resources, start / stop / restart, creation, deletion |
| `functions:deploy` | Code uploads, deployments and rollbacks, secrets, invoke credentials, variables, handler, networks, egress and MCP opt-in (`mcpInvocable`) of a function (with `networks:write` for the networks routes) |
| `functions:invoke` | `POST /functions/{name}/invoke`, and (with `cron`) the creation, change and manual run of `function_invoke` tasks. Implied by no other scope but `*` |
| `audit:read` | `GET /audit` |

The WebSocket `logsStop` and `statsStop` events need no scope: they only stop a stream the connection opened.

- **Profile**: a key carries the profile of its user, so an admin scope on a restricted user's key grants nothing.
- **Routes without scopes**: the user's own profile and keys, the user routes and `/domains` only check the profile. A key can only create keys within its own scopes.
- **Tokens**: `loch_` followed by 64 hexadecimal characters, valid up to 365 days, at most 20 keys per user.

## WebSocket

Connect to `/ws` with a session cookie or the `X-API-Key` header. Messages are JSON `{"event": ..., "data": ...}`.

Client messages:

```json
{"event": "attach", "data": {"id": "container-id"}}
{"event": "streamInput", "data": "command"}
{"event": "resize", "data": {"cols": 120, "rows": 40}}
{"event": "logsStart", "data": {"id": "container-id", "tail": 100, "file": "/optional/path"}}
{"event": "logsStop"}
{"event": "statsStart", "data": {"ids": ["container-id-1", "container-id-2"]}}
{"event": "statsStop"}
{"event": "proxyOpen", "data": {"id": "container-id", "mappings": [{"remote": 5432, "local": 15432}]}}
{"event": "streamOpen", "data": {"streamID": 1, "remotePort": 5432}}
{"event": "streamClose", "data": {"streamID": 1}}
```

Server events: `attach`, `stdout`, `stderr`, `end`, `logsData`, `logsEnd`, `logsError`, `statsData`, `statsError`, `proxyReady`, `streamOpened`, `streamClose`, `streamError`, `proxyError`, `error`.

- **Port proxy**:
  - data travels in binary messages `[streamID: uint32 big endian][payload]`;
  - at most 10 mappings per session and 256 concurrent streams.
- **Stats**:
  - collected every 2 seconds, for at most 20 containers per subscription;
  - one `statsStart` every 3 seconds at most.
