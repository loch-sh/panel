/**
 * Runtime configuration.
 *
 * Values are resolved in this order (first non-empty wins):
 *   1. `window.__APP_CONFIG__`, written to `/config.js` by the Docker entrypoint
 *      from the container environment (see docker/40-runtime-config.sh);
 *   2. `VITE_*` variables, for local development (`.env`);
 *   3. built-in defaults.
 */
export interface AppConfig {
  /** Display name (browser tab, terminal title, logo alt text). */
  appName: string;
  /** Logo shown in the sidebar. */
  logoUrl: string;
  /** Favicon URL. */
  faviconUrl: string;
  /** Base URL of the Host Panel API. */
  apiUrl: string;
  /** Base URL of the API WebSocket endpoint. */
  wsUrl: string;
  /** Support phone number shown on the home page (hidden when empty). */
  supportPhone: string;
  /** Support / incident ticket URL shown on the home page (hidden when empty). */
  supportUrl: string;
  /** Status page embedded on the home page (hidden when empty). */
  statusUrl: string;
}

declare global {
  interface Window {
    __APP_CONFIG__?: Partial<AppConfig>;
  }
}

const defaults: AppConfig = {
  appName: "Loch.sh Panel",
  logoUrl: "/logo.svg",
  faviconUrl: "/favicon.svg",
  apiUrl: "http://localhost:2538",
  wsUrl: "ws://localhost:2538",
  supportPhone: "",
  supportUrl: "",
  statusUrl: "",
};

const env = import.meta.env;

/** Development build (enables extra console warnings). */
export const isDev: boolean = env.DEV;

const fromEnv: Partial<AppConfig> = {
  appName: env.VITE_APP_NAME,
  logoUrl: env.VITE_LOGO_URL,
  faviconUrl: env.VITE_FAVICON_URL,
  apiUrl: env.VITE_API_URL,
  wsUrl: env.VITE_WS_URL,
  supportPhone: env.VITE_SUPPORT_PHONE,
  supportUrl: env.VITE_SUPPORT_URL,
  statusUrl: env.VITE_STATUS_URL,
};

function resolve(key: keyof AppConfig): string {
  const runtime = window.__APP_CONFIG__?.[key];
  if (runtime) return runtime;
  return fromEnv[key] || defaults[key];
}

export const appConfig: Readonly<AppConfig> = Object.freeze(
  Object.fromEntries(
    (Object.keys(defaults) as (keyof AppConfig)[]).map((key) => [
      key,
      resolve(key),
    ]),
  ) as unknown as AppConfig,
);
