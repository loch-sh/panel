#!/bin/sh
# Generates /config.js from the container environment so a single image can be
# rebranded and pointed at any API without rebuilding.
#
# Branding files (logo, favicon) can be mounted into /branding:
#   docker run -v ./my-logo.png:/branding/logo.png ...
# or referenced by URL with LOGO_URL / FAVICON_URL (takes precedence).
set -eu

HTML_DIR=/usr/share/nginx/html
BRANDING_DIR=/branding
OUT="$HTML_DIR/config.js"

# Escape a value for a JSON string literal: backslashes, double quotes, and
# drop control characters (newlines, tabs...) which have no place in config.
json_escape() {
  printf '%s' "$1" | tr -d '\000-\037' | sed -e 's/\\/\\\\/g' -e 's/"/\\"/g'
}

# First file named <name>.<ext> found in /branding, served under /branding/.
find_branding() {
  for ext in svg png webp jpg jpeg ico; do
    if [ -f "$BRANDING_DIR/$1.$ext" ]; then
      printf '/branding/%s.%s' "$1" "$ext"
      return
    fi
  done
}

LOGO_URL="${LOGO_URL:-$(find_branding logo)}"
FAVICON_URL="${FAVICON_URL:-$(find_branding favicon)}"
# Fall back to the mounted logo when no dedicated favicon is provided.
FAVICON_URL="${FAVICON_URL:-$LOGO_URL}"

entry() {
  printf '  "%s": "%s",\n' "$1" "$(json_escape "$2")"
}

{
  echo "// Generated at container start by 40-runtime-config.sh. Do not edit."
  echo "window.__APP_CONFIG__ = {"
  entry appName "${APP_NAME:-}"
  entry logoUrl "$LOGO_URL"
  entry faviconUrl "$FAVICON_URL"
  entry apiUrl "${API_URL:-}"
  entry wsUrl "${WS_URL:-}"
  entry supportPhone "${SUPPORT_PHONE:-}"
  entry supportUrl "${SUPPORT_URL:-}"
  entry statusUrl "${STATUS_URL:-}"
  echo "};"
} > "$OUT"

echo "40-runtime-config.sh: wrote $OUT"
