# syntax=docker/dockerfile:1

# --- Build ---------------------------------------------------------------
# The build output is static: compile once on the build platform.
FROM --platform=$BUILDPLATFORM mirror.gcr.io/library/node:26-alpine AS build
WORKDIR /app

# Node >= 25 no longer bundles corepack (nor yarn) in the official image
RUN npm install -g corepack && corepack enable

COPY package.json yarn.lock .yarnrc.yml* ./
RUN yarn install --immutable

COPY . .
RUN yarn build

# --- Runtime -------------------------------------------------------------
FROM mirror.gcr.io/library/nginx:1.31-alpine

COPY --from=build /app/dist /usr/share/nginx/html
COPY .nginx/default.conf /etc/nginx/conf.d/default.conf
COPY --chmod=0755 docker/40-runtime-config.sh /docker-entrypoint.d/40-runtime-config.sh

# Mount branding files here (logo.svg|png|webp|jpg, favicon.*)
RUN mkdir -p /branding
VOLUME ["/branding"]

# Runtime configuration (all optional), see README:
#   APP_NAME, LOGO_URL, FAVICON_URL, API_URL, WS_URL,
#   SUPPORT_PHONE, SUPPORT_URL, STATUS_URL
EXPOSE 80
