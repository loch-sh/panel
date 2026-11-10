# Audit trail

Every action on the panel is recorded in `<API_LOG_DIR>/audit.log` (default `./log/audit.log`). In the Docker image it is `/app/log`, declared as a volume: mount it (`-v /srv/loch/log:/app/log`) to keep the trail when the container is recreated. `make docker-run` does not mount it.

## What is recorded

- **Business events**:
  - logins, logouts and failed logins;
  - container and stack actions, shell, logs, port proxy, file copies;
  - scheduled tasks, users, API keys, SFTP users, sites, managed services, private networks (`network created`, `network member attached`, `network container disconnected`...) published ports (`service exposure changed`) and updates of dedicated instances (`service instance updated`, `service instance update rolled back`);
  - consultations of the audit trail itself.
- **Requests**, as a safety net, whether or not a handler records its own event:
  - every write request (`api request`: method, path without query string, status, user, API key id, client IP);
  - every refused request (`access denied`: 403, or 401 with an API key);
  - sensitive reads: container inspection (it shows environment variables), site settings, file downloads and file tails.

## Limits on what is written

- **No secrets**: no secret is ever written, neither password, token, header nor request body.
- **Bounded values**: every text value is cut at 2 KB.
- **Anti-flooding**: unauthenticated refusals are aggregated to one entry per IP and per minute, and requests to unknown routes are ignored, so an anonymous client cannot fill the trail.
- **Lost writes**: a write that fails (disk full, file removed) is reported on the standard error.

The format is one JSON object per line: `time`, `level`, `msg` (the event) and structured fields.

## Consulting it

- **Web interface**: the "Security → Audit trail" menu, for admins only. It offers filters by date range (with shortcuts), event, level and user, and a full-text search.
- **API**: `GET /audit`, admin only (API keys need the `audit:read` scope), newest entries first, with a cursor for older pages (a page may be shorter than requested: follow the cursor). See the [API reference](api.md#audit-trail).

## Protection

- **No write route**: the trail can only be read from the panel, never changed or deleted.
- **The API container is protected**: shell, file copy, file tail (REST and WebSocket) and `exec` scheduled tasks are refused on it, since they would give access to `audit.log`.
- **Out of reach of the panel**: someone with root on the host can still change the file. Ship it to a remote syslog, or use `chattr +a`, if you need stronger integrity.
- **Rotation**: the file is not rotated by the API.
