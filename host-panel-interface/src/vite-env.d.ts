/// <reference types="vite/client" />

interface ImportMetaEnv {
  readonly VITE_APP_NAME?: string;
  readonly VITE_LOGO_URL?: string;
  readonly VITE_FAVICON_URL?: string;
  readonly VITE_API_URL?: string;
  readonly VITE_WS_URL?: string;
  readonly VITE_SUPPORT_PHONE?: string;
  readonly VITE_SUPPORT_URL?: string;
  readonly VITE_STATUS_URL?: string;
}

interface ImportMeta {
  readonly env: ImportMetaEnv;
}
