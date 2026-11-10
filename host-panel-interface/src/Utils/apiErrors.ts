import { isAxiosError } from "axios";
import i18n from "../i18n";
import type enErrors from "../i18n/locales/en/errors.json";

type ApiErrorCode = keyof typeof enErrors.codes;

function isKnownErrorCode(code: string): code is ApiErrorCode {
  return i18n.exists(`errors:codes.${code}`);
}

/**
 * Extracts a user-facing error message from an API error response.
 * Uses the machine-readable `code` field mapped to a translated message
 * (`errors` namespace, resolved at call time so the current language is used).
 * Falls back to a generic message — the raw `error` field is never surfaced to
 * avoid leaking internal details.
 */
export function getApiErrorMessage(err: unknown, fallback?: string): string {
  const fallbackMessage = fallback ?? i18n.t("errors:fallback");
  if (!isAxiosError(err)) return fallbackMessage;

  const data = err.response?.data;
  if (!data || typeof data !== "object") return fallbackMessage;

  const code = data.code as string | undefined;
  if (code && isKnownErrorCode(code)) return i18n.t(`errors:codes.${code}`);

  if (import.meta.env.DEV) {
    console.warn("Unmapped API error code:", code, data.error);
  }

  return fallbackMessage;
}
