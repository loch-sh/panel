#!/bin/sh
# Starts the stack: creates the shared traefik_default network if it is
# missing, then runs `docker compose up -d`. Extra arguments are passed to
# `docker compose up` (e.g. ./up.sh sftpgo); the URLs of the stack are printed at the end.
#
# traefik_default stays `external` in compose.yaml: hosted sites and CI stacks
# use it too, so `docker compose down` must never remove it.
#
# With SFTP_ENABLED and no SFTP_API_KEY in app.env the API refuses to start
# (restart loop). In a terminal, up.sh offers to create the SFTPGo admin API
# key (asks for the admin's username and password) and writes it to app.env;
# otherwise only SFTPGo is started, to create the key by hand (README).
#
# On macOS without DATA_ROOT, the host directories go to ./data.
set -eu

cd "$(dirname "$0")"

SFTPGO_URL=http://127.0.0.1:8081

for file in .env app.env; do
  if [ ! -f "$file" ]; then
    echo "missing $file: cp $file.example $file, then edit it" >&2
    exit 1
  fi
done

# Last value of a variable in an env file (Compose keeps the last one),
# without surrounding spaces or quotes.
env_value() {
  sed -n "s/^[[:space:]]*$2[[:space:]]*=//p" "$1" | tail -n 1 |
    sed -e 's/^[[:space:]]*//' -e 's/[[:space:]]*$//' -e 's/^"\(.*\)"$/\1/' -e "s/^'\(.*\)'$/\1/"
}

app_env() {
  env_value app.env "$1"
}

# macOS (Docker Desktop, OrbStack): /data cannot be shared with the Docker VM.
# Without DATA_ROOT (shell or .env), use ./data next to this script; Compose
# reads the exported value for compose.yaml and app.env.
if [ -z "${DATA_ROOT:-}" ] && [ -z "$(env_value .env DATA_ROOT)" ] && [ "$(uname -s)" = Darwin ]; then
  DATA_ROOT="$(pwd)/data"
  export DATA_ROOT
  echo "macOS: DATA_ROOT not set, using $DATA_ROOT (set DATA_ROOT in .env to change it)"
fi

# Prints the URLs of the started stack, from the services of compose.yaml
# and DOMAIN (shell environment first, as Compose does, then .env).
show_urls() {
  domain=${DOMAIN:-$(env_value .env DOMAIN)}
  services=$(docker compose config --services 2>/dev/null) || return 0
  has() { printf '%s\n' "$services" | grep -qx "$1"; }
  echo
  echo "Loch.sh Panel is starting:"
  if has interface; then
    echo "  Interface          https://manager.$domain  (the first user to log in becomes admin)"
  fi
  if has api; then
    echo "  API                https://manager.api.$domain"
  fi
  if has deploy; then
    echo "  deploy-server      https://deploy.$domain"
  fi
  if has sftpgo; then
    echo "  SFTP               sftp://ftp.$domain:2022"
    echo "  SFTP web client    https://ftp.$domain/web/client"
    echo "  SFTPGo admin       $SFTPGO_URL/web/admin  (host loopback; remote: ssh -L 8081:127.0.0.1:8081 <host>)"
  fi
  case "$domain" in
    *.traefik.me)
      echo "Local run (traefik.me): Traefik serves a self-signed certificate. Open the API URL once"
      echo "and accept it, then the interface: until then the browser blocks the API calls (blank page)."
      ;;
  esac
  echo "Logs: docker compose logs -f api  (the API lists every configuration problem at startup)"
}

# Sets a variable in app.env (every assignment replaced, appended if absent).
# The file is rewritten in place to keep its owner and permissions.
set_app_env() {
  tmp=$(mktemp app.env.XXXXXX)
  NAME=$1 VALUE=$2 awk '
    $0 ~ "^[[:space:]]*" ENVIRON["NAME"] "[[:space:]]*=" { print ENVIRON["NAME"] "=" ENVIRON["VALUE"]; done = 1; next }
    { print }
    END { if (!done) print ENVIRON["NAME"] "=" ENVIRON["VALUE"] }
  ' app.env >"$tmp"
  cat "$tmp" >app.env
  rm -f "$tmp"
}

# Escapes a string for a JSON string or a curl config value.
escape() {
  printf '%s' "$1" | sed -e 's/\\/\\\\/g' -e 's/"/\\"/g'
}

sftp_instructions() {
  cat >&2 <<'EOF'

Only SFTPGo was started. Create its admin API key (README, "Installation", step 3):
  1. open http://localhost:8081/web/admin (from a remote host: ssh -L 8081:127.0.0.1:8081 <host>),
     create the admin, then enable "API key authentication" in its profile
  2. create the key with the REST API (commands in the README), or run ./up.sh again
     in a terminal and let it create the key
  3. set SFTP_API_KEY in app.env and run ./up.sh again
Or set SFTP_ENABLED=false in app.env to start without the SFTP plugin.
EOF
}

# Waits for SFTPGo's admin binding (published on the host loopback).
wait_sftpgo() {
  i=0
  until curl -fs -o /dev/null "$SFTPGO_URL/healthz"; do
    i=$((i + 1))
    if [ "$i" -ge 60 ]; then
      echo "SFTPGo does not answer on $SFTPGO_URL (docker compose logs sftpgo)" >&2
      return 1
    fi
    sleep 1
  done
}

# Asks for the SFTPGo admin credentials, enables API key authentication on
# its profile, creates an API key and writes it to app.env. The password is
# read without echo and given to curl through its stdin config (never in the
# process arguments).
create_sftp_key() {
  echo
  echo "If not done yet, create the SFTPGo admin first: open http://localhost:8081/web/admin"
  attempt=0
  while :; do
    attempt=$((attempt + 1))
    printf 'SFTPGo admin username: '
    IFS= read -r admin
    # Echo off before the prompt: a password typed ahead must not show.
    trap 'stty echo; echo; exit 130' INT TERM
    stty -echo
    printf 'Password: '
    IFS= read -r password || true
    stty echo
    trap - INT TERM
    echo

    response=$(printf 'user = "%s"\n' "$(escape "$admin:$password")" |
      curl -s -K - -w '\n%{http_code}' "$SFTPGO_URL/api/v2/token") || response=$(printf '\n000')
    password=
    status=$(printf '%s' "$response" | tail -n 1)
    if [ "$status" = 200 ]; then
      break
    fi
    echo "authentication failed (HTTP $status)" >&2
    if [ "$attempt" -ge 3 ]; then
      return 1
    fi
  done
  token=$(printf '%s' "$response" | sed -n 's/.*"access_token":"\([^"]*\)".*/\1/p')
  if [ -z "$token" ]; then
    echo "no access token in SFTPGo's answer" >&2
    return 1
  fi

  # API key authentication must be allowed on the admin: keep the rest of its
  # profile (PUT replaces it).
  profile=$(printf 'header = "Authorization: Bearer %s"\n' "$token" |
    curl -fs -K - "$SFTPGO_URL/api/v2/admin/profile") || {
    echo "cannot read the admin profile" >&2
    return 1
  }
  case "$profile" in
    *'"allow_api_key_auth":true'*) ;;
    *)
      profile=$(printf '%s' "$profile" |
        sed -e 's/"allow_api_key_auth":false,\{0,1\}//' -e 's/^{/{"allow_api_key_auth":true,/' -e 's/,}$/}/')
      printf 'header = "Authorization: Bearer %s"\n' "$token" |
        curl -fs -o /dev/null -K - -X PUT -H 'Content-Type: application/json' \
          --data-binary "$profile" "$SFTPGO_URL/api/v2/admin/profile" || {
        echo "cannot enable API key authentication on the admin profile" >&2
        return 1
      }
      echo "API key authentication enabled on the admin profile"
      ;;
  esac

  body=$(printf '{"name":"loch-panel %s","description":"Loch.sh Panel (SFTP_API_KEY)","scope":1,"admin":"%s"}' \
    "$(date +%Y-%m-%d)" "$(escape "$admin")")
  answer=$(printf 'header = "Authorization: Bearer %s"\n' "$token" |
    curl -fs -K - -X POST -H 'Content-Type: application/json' \
      --data-binary "$body" "$SFTPGO_URL/api/v2/apikeys") || {
    echo "cannot create the API key" >&2
    return 1
  }
  key=$(printf '%s' "$answer" | sed -n 's/.*"key":"\([^"]*\)".*/\1/p')
  case "$key" in
    '' | *[!A-Za-z0-9._-]*)
      echo "unexpected answer from SFTPGo, no key written" >&2
      return 1
      ;;
  esac
  set_app_env SFTP_API_KEY "$key"
  echo "API key created and written to app.env (SFTP_API_KEY)"
}

if ! docker network inspect traefik_default >/dev/null 2>&1; then
  echo "creating network traefik_default"
  docker network create traefik_default >/dev/null
fi

sftp_enabled=$(app_env SFTP_ENABLED | tr '[:upper:]' '[:lower:]')
case "$sftp_enabled" in
  true | 1 | yes | on)
    if [ -z "$(app_env SFTP_API_KEY)" ] && [ "$*" != "sftpgo" ]; then
      echo "SFTP_ENABLED is set but SFTP_API_KEY is empty in app.env: the API would refuse to start." >&2
      if ! docker compose config --services | grep -qx sftpgo; then
        echo "Set SFTP_API_KEY (SFTPGo admin API key) or SFTP_ENABLED=false in app.env." >&2
        exit 1
      fi
      docker compose up -d sftpgo
      if [ ! -t 0 ] || [ ! -t 1 ]; then
        sftp_instructions
        exit 1
      fi
      printf 'Create a new SFTPGo admin API key now? [y/N] '
      IFS= read -r reply || reply=
      case "$reply" in
        [yY] | [yY][eE][sS]) ;;
        *)
          sftp_instructions
          exit 1
          ;;
      esac
      wait_sftpgo
      if ! create_sftp_key; then
        sftp_instructions
        exit 1
      fi
    fi
    ;;
esac

docker compose up -d "$@"
show_urls
