# Loch.sh Panel — web interface

Web interface of **Loch.sh Panel**, a self-hosted panel to manage Docker containers:
browse Compose stacks, start/stop/restart containers, open a terminal, stream logs,
watch resource usage, manage scheduled tasks (cron), routed domains, users, API keys
and SFTP accounts. Administrators also manage hosted sites, managed services
(databases, caches, queues, storage: networks and published ports included) and
private networks (internal Docker networks: create them, attach sites and service
instances, see the members with their IP addresses, disconnect foreign containers).

It is a single-page application (React 19, TypeScript, Ant Design 6, Vite) that talks
to the **Host Panel API** (Go). The API handles authentication (OIDC) and Docker access;
this interface only needs to know where the API is.

- **Languages:** English, French, German, Spanish, Italian, Dutch, Portuguese
  (Portugal and Brazil), Japanese and Simplified Chinese. The browser language is
  detected; users can switch from the sidebar.
- **White-label:** name, logo, favicon and support links are configured when the
  container starts — one image for every deployment.

## Quick start (Docker)

```bash
docker build -t loch-panel-interface .

docker run -d -p 8080:80 \
  -e API_URL=https://panel-api.example.com \
  -e WS_URL=wss://panel-api.example.com \
  loch-panel-interface
```

Then open http://localhost:8080. The API must allow this origin (`FRONT_URL` on the API side).

## Configuration

All settings are optional environment variables, read **at container start**
(no rebuild needed). They are written to `/config.js`, loaded before the app.

| Variable        | Default                  | Description                                                        |
| --------------- | ------------------------ | ------------------------------------------------------------------ |
| `API_URL`       | `http://localhost:2538`  | Base URL of the Host Panel API.                                    |
| `WS_URL`        | `ws://localhost:2538`    | Base URL of the API WebSocket (logs, terminal, proxy).             |
| `APP_NAME`      | `Loch.sh Panel`          | Name shown in the browser tab, terminal windows and logo alt text. |
| `LOGO_URL`      | `/logo.svg`              | Logo URL. Overrides a mounted logo file.                           |
| `FAVICON_URL`   | logo, else `/favicon.svg`| Favicon URL. Overrides a mounted favicon file.                     |
| `SUPPORT_PHONE` | —                        | Support phone number on the home page (hidden when empty).         |
| `SUPPORT_URL`   | —                        | Support / incident ticket link on the home page (hidden when empty).|
| `STATUS_URL`    | —                        | Status page embedded on the home page (hidden when empty).         |

### Custom logo

Either mount files into `/branding` (first match wins: `svg`, `png`, `webp`, `jpg`, `jpeg`, `ico`):

```bash
docker run -d -p 8080:80 \
  -v ./my-logo.png:/branding/logo.png:ro \
  -v ./my-favicon.svg:/branding/favicon.svg:ro \
  -e API_URL=https://panel-api.example.com \
  -e WS_URL=wss://panel-api.example.com \
  loch-panel-interface
```

or point to an existing URL with `LOGO_URL` / `FAVICON_URL`. Without a dedicated
favicon, the mounted logo is used as favicon. The logo is displayed square-ish
(80 px wide, 40 px when the sidebar is collapsed); SVG or a transparent PNG works best.

### Docker Compose

```yaml
services:
  panel:
    image: loch-panel-interface
    ports:
      - "8080:80"
    environment:
      API_URL: https://panel-api.example.com
      WS_URL: wss://panel-api.example.com
      APP_NAME: My Hosting Panel
      SUPPORT_URL: https://support.example.com
    volumes:
      - ./branding:/branding:ro # logo.svg, favicon.svg
```

## Development

Requirements: Node.js 26 and Yarn 4 (`corepack enable`), a running Host Panel API.

```bash
cp .env.example .env   # API URL, optional branding (VITE_* variables)
yarn install
yarn dev               # http://localhost:5173
```

In development, configuration comes from `VITE_*` variables in `.env` (same names
as above, with the `VITE_` prefix). Files placed in `public/branding/` are served at
`/branding/` (this folder is git-ignored), e.g. `VITE_LOGO_URL=/branding/logo.png`.

| Command               | Description                                         |
| --------------------- | --------------------------------------------------- |
| `yarn dev`            | Development server with hot reload.                 |
| `yarn build`          | Type-check and production build (`dist/`).          |
| `yarn lint`           | ESLint.                                             |
| `yarn i18n:check`     | Check that every language has the same keys as English. |
| `yarn typecheck`      | Type-check the app and the tests.                   |
| `yarn test`           | Unit and component tests (Vitest, jsdom).           |
| `yarn test:watch`     | Vitest in watch mode.                               |
| `yarn coverage`       | Vitest with coverage (`coverage/`: HTML, Cobertura). |
| `yarn test:e2e`       | End-to-end tests (Playwright, Chromium).            |

## Tests

Both suites run against a **mocked API**: no Host Panel API, no network. The
mock is an in-memory implementation of the routes the interface calls
(`tests/mocks/api.ts`), with typed fixtures (`tests/mocks/fixtures.ts`) and a
state each test can change. It is shared by the two suites:

- **Vitest** (`src/**/*.test.ts(x)`): utilities and components in jsdom with
  Testing Library. [MSW](https://mswjs.io) serves the mock
  (`tests/setup/msw.ts`); `renderWithProviders` (`tests/setup/render.tsx`)
  mounts a component with React Query and a router.
- **Playwright** (`tests/e2e/*.spec.ts`): the whole app in Chromium, built
  with `VITE_API_URL=http://api.test` and served by `vite preview`
  (`dist-e2e/`); every request to that origin is answered by `page.route()`
  (`tests/e2e/fixtures.ts`), the WebSocket is accepted without a server, and a
  request to any other origin is refused.

In both, a test **fails if the interface calls a route the mock does not
serve**: add the route to `tests/mocks/api.ts`, or override it in the test:

```ts
test("shows the API error", async ({ page, api }) => {
  api.on("POST", "/services/resources", {
    status: 409,
    body: { code: "RESOURCE_EXISTS" },
  });
  api.state.me = restrictedUser; // change the mocked state
  // ...
  expect(api.calls("POST", "/services/resources")[0].body).toEqual({ ... });
});
```

First run of the end-to-end tests: `npx playwright install chromium`. On
failure, the trace is in `test-results/` (`npx playwright show-trace <zip>`).

## Translations

Translations live in `src/i18n/locales/<language>/<namespace>.json`, one namespace
per area of the app (`common`, `layout`, `containers`, `cron`, `sftp`, …).

- **English is the source of truth.** Keys are typed from the English files, so
  using an unknown key fails the type-check.
- Other languages must have exactly the same keys, `{{placeholders}}` and `<Trans>`
  tags; `yarn i18n:check` enforces it (also run in CI), and checks that every
  language in `SUPPORTED_LANGUAGES` has a locale folder.
- Plurals use i18next suffixes (`_one`, `_other`, plus `_many` where the language needs it).

Language codes are BCP 47: plain codes (`fr`, `ja`) unless regional variants
differ (`pt-PT` / `pt-BR`) or the script matters (`zh-CN`, Simplified Chinese).
Browser codes resolve to the closest match (`fr-CA` → `fr`, `pt` → `pt-PT`,
`zh-TW` → `zh-CN`), unsupported languages fall back to English.

**Adding a language** (e.g. Korean `ko`):

1. Copy `src/i18n/locales/en/` to `src/i18n/locales/ko/` and translate the values
   (keep keys, `{{placeholders}}` and `<tags>` unchanged).
2. Add `"ko"` to `SUPPORTED_LANGUAGES` in `src/i18n/index.ts` (and a `fallbackLng`
   entry if a bare code must map to a regional one).
3. Add the matching date-fns, antd and cronstrue locales in `src/i18n/useLocales.ts`.
4. Add the language name (written in that language) under `language` in every `common.json`.
5. Run `yarn i18n:check` and `yarn build`.

## Project structure

```
src/
├── config.ts            # Runtime configuration (window.__APP_CONFIG__ / VITE_* / defaults)
├── i18n/                # i18next setup, typed keys, locale files
├── Components/          # Shared components (layout, side panels, terminal, logs…)
├── Pages/<Area>/        # One folder per page
└── Services/<Area>/     # API calls (service.ts), models, React Query hooks
docker/40-runtime-config.sh  # Generates /config.js at container start
.nginx/default.conf          # nginx config (SPA routing, caching, /branding)
```

## Contributing

Issues and merge requests are welcome. Before submitting, please run:

```bash
yarn lint && yarn i18n:check && yarn build
```

User-facing text must go through translations (no hardcoded strings in components);
add the keys to all languages — English is required, others can be machine-assisted
and reviewed by native speakers.

## Trademarks

MySQL is a registered trademark of Oracle and/or its affiliates. MariaDB is a registered trademark of MariaDB plc. PostgreSQL is a registered trademark of the PostgreSQL Community Association of Canada. MongoDB is a registered trademark of MongoDB, Inc. Redis is a registered trademark of Redis Ltd. Valkey and NATS are trademarks of The Linux Foundation. RabbitMQ is a registered trademark of Broadcom, Inc. Eclipse Mosquitto is a trademark of the Eclipse Foundation. Amazon SQS, Amazon S3, AWS, AWS Lambda and Amazon Linux are trademarks of Amazon.com, Inc. or its affiliates; Loch.sh is not affiliated with or endorsed by Amazon, and these names are used only to describe compatibility. All other names are the property of their respective owners; their use does not imply any endorsement.

## License

Copyright 2026 Loch.sh. Licensed under the [Apache License, Version 2.0](../LICENSE).
