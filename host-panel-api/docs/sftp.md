# SFTP users

Optional plugin (web agency mode) creating SFTP users through [SFTPGo](https://github.com/drakkan/sftpgo), to give site owners access to their files. Admins only.

- **Site**: a directory under `SFTP_DIRECTORY`, shared by one or more users. It is created with its first user.
- **Sub-directory**: a user can be confined to a sub-directory of the site, chosen at creation and fixed afterwards.
- **No local store**: SFTPGo is the only source of truth, the panel stores nothing.
- **Scope**: only the SFTPGo users whose home directory is under `SFTP_DIRECTORY` are shown and managed; the others are ignored.

## Configuration

| Variable | Description | Default |
|---|---|---|
| `SFTP_ENABLED` | Enables the plugin | `false` |
| `SFTP_DIRECTORY` | Base directory of the sites | required |
| `SFTP_API_URL` | Internal URL of the SFTPGo admin API | required |
| `SFTP_API_KEY` | SFTPGo admin API key | required |
| `SFTP_DOMAIN`, `SFTP_PORT` | Connection details shown to the users | — |
| `SFTP_WEB_USER_INTERFACE` | URL of the SFTPGo web client shown to the users | — |
| `SFTP_INIT_DIRS` | Directories created in the home of a new user (empty: none) | `web,logs` |

## Enabling it in development

1. Start SFTPGo from the group directory: `make sftpgo`.
2. Create an admin API key: `cd host-panel-api && make sftp-key`. The admin password is asked interactively.
   - Alternative: create it in the SFTPGo admin interface (http://localhost:8080/web/admin, default credentials `admin` / `sftpgo-admin`), linked to its admin.
3. Set the variables in `.env`:

   ```env
   SFTP_ENABLED=true
   SFTP_DIRECTORY=/data/websites/hosted
   SFTP_API_URL=http://localhost:8080
   SFTP_API_KEY=<the admin API key>
   SFTP_DOMAIN=localhost
   SFTP_PORT=2222
   SFTP_WEB_USER_INTERFACE=http://localhost:8080/web/client
   ```

4. Restart the API: the "SFTP" menu appears for admins.

## With the CLI

```bash
loch sftp sites                       # sites
loch sftp users [--site <name>]       # users
loch sftp create <username> --site <name> [--subpath <path>] [--init-dirs=false] \
  [--password <pw>] [--ssh-key <key>] [--quota-size <size>] [--quota-files <n>] [--bandwidth <kbps>] [--must-change-password]
loch sftp delete <username>           # asks for confirmation (--yes to skip)
loch sftp reset-password <username>
loch sftp enable|disable <username>
```

At creation, the directories of `SFTP_INIT_DIRS` are created in the user's home through SFTPGo. This is best effort: a failure never cancels the creation.

## Safety

- **Home directory**: the API builds it from the site and sub-directory; neither can leave `SFTP_DIRECTORY`.
  - Each path segment is checked: no `.`, `..`, separator or control character. A leading dot stays allowed, for `.well-known` and similar.
- **Passwords**: they are never logged nor returned.
- **Audit**: every operation is recorded in the audit trail.
