import i18n from "../i18n";

// Unit symbols are locale conventions rather than translatable text: French
// uses "octet" (o, ko, Mo…), the other supported languages use bytes (B, kB…).
const FRENCH_UNITS = ["o", "ko", "Mo", "Go", "To"];
const BYTE_UNITS = ["B", "kB", "MB", "GB", "TB"];

/** Formats a byte count into a human-readable string (binary units, current language). */
export function formatBytes(bytes: number): string {
  const lang = i18n.resolvedLanguage ?? i18n.language;
  const units = lang === "fr" ? FRENCH_UNITS : BYTE_UNITS;
  if (!bytes || bytes <= 0) return `0 ${units[0]}`;
  const exponent = Math.min(
    Math.floor(Math.log(bytes) / Math.log(1024)),
    units.length - 1
  );
  const value = bytes / Math.pow(1024, exponent);
  const digits = value >= 100 || exponent === 0 ? 0 : 1;
  const formatted = value.toLocaleString(lang, {
    minimumFractionDigits: digits,
    maximumFractionDigits: digits,
  });
  return `${formatted} ${units[exponent]}`;
}

export type ByteUnit = "MB" | "GB";

/** Converts a value expressed in MB/GB into bytes (binary). */
export function toBytes(value: number, unit: ByteUnit): number {
  const factor = unit === "GB" ? 1024 ** 3 : 1024 ** 2;
  return Math.round(value * factor);
}
