# Security

Security architecture of the API. The managed services (databases, cache, messaging) have their own page: [Managed services security](services-security.md).

To report a vulnerability, contact the maintainers privately rather than opening a public issue.

## Authentication

### Browser sessions (OIDC)

- **Providers**: Microsoft Entra ID, Keycloak, or any OIDC-compliant provider.
- **Flow**: Authorization Code with PKCE. The `state` parameter is checked in the callback, which uses `response_mode=query`.
- **Tokens**: RS256 JWTs verified against the provider's JWKS.
- **Cookies**:
  - the session cookie is HMAC-SHA256 signed, `httpOnly`, `Secure`, `SameSite=None`, valid one hour;
  - the login cookie, used during the five minutes of the login, is `SameSite=Lax`;
  - `COOKIE_SECRET` must have at least 32 characters.

### API keys (CLI and scripts)

- **Token**: `loch_` followed by 64 hexadecimal characters (256 bits from `crypto/rand`), sent in `X-API-Key`.
- **Storage**: only the SHA-256 hash is stored, and keys are looked up by it. The token is shown once, at creation; the list shows its first 13 characters to identify it.
- **Lifetime**: 1 to 365 days, checked on every request.
- **Limits**: at most 20 keys per user. Deleting a user revokes its keys.
- **Mixed requests**: `X-API-Key` is checked first; requests without it go through the session cookie. Both paths load the same user.

## Authorization

| Profile | Containers | Admin routes |
|---|---|---|
| `admin` | all | allowed |
| `restricted` | only those listed in its permissions | refused (403) |

- **Profiles**: the first user to log in becomes admin, the next ones are restricted.
- **Admin routes**: users, domains, SFTP, hosting, services, private networks and the audit trail are reserved to admins, whatever the scopes of an API key.
- **Scopes**: an API key can be limited to some features (see [API key scopes](api.md#api-key-scopes)). `*` cannot be combined with other scopes, unknown scopes are refused, and legacy keys without scopes keep full access.
  - Scopes do not apply to the user's own profile and keys, nor to the user and domain routes, which only check the admin profile.
  - A key can only create keys within its own scopes: a limited key cannot mint a full one.
- **Container access** is checked at every layer: authentication and profile, then the key scope, then the container permissions. WebSocket events inspect the container to resolve its name before checking.
- **The API container is protected** from every user: shell, file copy, file tail and `exec` scheduled tasks are refused on it (they would give access to the audit log).

## Cross-site requests (CSRF)

The session cookie is `SameSite=None`, so the API checks the origin of every write request (`POST`, `PUT`, `PATCH`, `DELETE`).

- **Allowed**: a request from a browser must carry the `Origin` of `FRONT_URL`, compared on scheme, host and port.
- **Refused** with `403 FORBIDDEN_ORIGIN`: another origin, or a request marked `Sec-Fetch-Site: cross-site`.
- **Not concerned**: requests without these headers, such as the CLI, the MCP server and scripts, which cannot carry a victim's cookie anyway.
- **WebSocket**: it checks the same normalized origin.

## HTTP hardening

- **Headers**:
  - `Strict-Transport-Security: max-age=63072000; includeSubDomains`;
  - `X-Frame-Options: DENY`;
  - `X-Content-Type-Options: nosniff`;
  - `X-XSS-Protection: 0` (the legacy browser filter is disabled, as recommended);
  - `Referrer-Policy: strict-origin-when-cross-origin`.
- **CORS**: only `FRONT_URL`, with credentials.
- **Rate limiting**: 120 requests per minute per IP, including the WebSocket upgrade.
  - The client IP is taken from `X-Forwarded-For` only behind a trusted proxy: the loopback, plus the addresses listed in `TRUSTED_PROXIES` (empty by default). Trusting a whole Docker range would let any container forge its IP, for the rate limit and the audit trail.
  - It is read from the right, skipping trusted proxies, so that a client cannot choose its own IP.
- **Request limits**:
  - request bodies: 1 MB (API keys, users, scheduled tasks, SFTP), 4 KB for a service creation;
  - WebSocket messages: 64 KB, 128 KB during a proxy session;
  - log tails: 10,000 lines;
  - terminal size: 500 × 200.
- **Scheduled HTTP tasks** cannot reach private or internal addresses, checked again after the connection (SSRF).
  - Their errors never include the URL, which may carry a token.
- **File copy**:
  - the container path must be absolute, with no null byte or newline, 4096 characters at most;
  - archives are streamed without buffering;
  - symbolic links are refused when absolute and checked against the target directory when relative.
- **Shutdown** on `SIGINT` / `SIGTERM`: the server stops accepting connections and closes the Docker client.

## Private networks

Hosted sites and service instances join named internal Docker networks ([Private networks](networks.md)). They separate the panel's sites and services from each other; they are **not** an access control against CI stacks or anything that can use the Docker socket, since any container can join a network by name. The panel verifies each network (Docker ID, `internal`, labels, subnet) before trusting its members and shows the containers it does not manage so that an admin can disconnect them. Details in [Managed services security](services-security.md#networks-and-exposure).

## Port proxy

The proxy opens TCP tunnels from the CLI to containers through a single WebSocket, so that it works through enterprise HTTP proxies (all traffic is HTTPS).

| Threat | Mitigation |
|---|---|
| Access to another container | Container permission checked on `proxyOpen` |
| Port enumeration | Only the ports declared in `proxyOpen` can be opened |
| Interception | Everything goes through WSS; payloads are never logged |
| Resource exhaustion | 10 mappings per session, 256 concurrent streams |
| Forged stream IDs | Unknown IDs are dropped |
| SSRF | Connections only go to the inspected container's IP on its Docker network |
| Internal IP leak | Clients get generic errors ("port not reachable") |
| Non-deterministic network choice | When the target has several networks, the proxy picks them in name order |

- **Frames**:
  - control messages are JSON;
  - data frames are binary, `[stream ID: uint32 big endian][payload]`, relayed byte for byte.
- **Stream IDs**:
  - the CLI assigns them (from 1, never 0);
  - a slot is reserved before dialing, so that two streams cannot race for the same ID.
- **Networks**: when the API and the container are on different networks, the API joins the container's network temporarily and leaves when the last stream closes.

## Data storage

Users (`users.json`), API keys (`apikeys.json`) and scheduled tasks (`cron.json`) are JSON files in `DATA_DIR`. They are written atomically (temporary file, then rename) and protected by a read-write lock.

| Data | Stored as | Exposed |
|---|---|---|
| API key token | never stored | only in the creation response |
| API key hash | SHA-256 | never |
| OIDC token | signed cookie | `httpOnly`, not readable by JavaScript |
| Proxy data | not stored | relayed in memory |

## Audit trail

Every action, refusal and sensitive read is recorded without secrets, and can only be consulted. See [Audit trail](audit.md).
