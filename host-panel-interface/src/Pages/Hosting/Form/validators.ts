/**
 * Client-side mirrors of the API validation rules. They only improve the UX:
 * the API stays the source of truth.
 */

const DOMAIN_LABEL = /^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$/;
const ENV_NAME = /^[A-Za-z_][A-Za-z0-9_]*$/;
const PHP_SIZE = /^([1-9][0-9]{0,4})([KMG])$/;

const MANAGED_ENV_VARS = [
  "PHP_MEMORY_LIMIT",
  "PHP_UPLOAD_MAX_FILESIZE",
  "PHP_POST_MAX_SIZE",
  "PHP_MAX_EXECUTION_TIME",
  "PHP_DISPLAY_ERRORS",
  "PHP_DATE_TIMEZONE",
  "PHP_DISMOD",
  "WEB_DOCUMENT_ROOT",
  "WEB_PHP_TIMEOUT",
];
const RESERVED_ENV_PREFIXES = ["APPLICATION_", "CONTAINER_", "LD_"];

/** Lowercase host name with at least one dot, no wildcard. */
export function isValidDomain(domain: string): boolean {
  if (domain.length > 253 || !domain.includes(".")) return false;
  return domain.split(".").every((label) => DOMAIN_LABEL.test(label));
}

export function isValidEnvName(name: string): boolean {
  return name.length <= 128 && ENV_NAME.test(name);
}

export function isReservedEnvName(name: string): boolean {
  const upper = name.toUpperCase();
  return (
    MANAGED_ENV_VARS.includes(upper) ||
    RESERVED_ENV_PREFIXES.some((p) => upper.startsWith(p))
  );
}

/** Allowed characters of a path segment (allow-list, same as the API). */
const PATH_SEGMENT = /^[A-Za-z0-9._@+~-]+$/;

/**
 * Relative path made of allowed segments separated by "/" ("" = site root):
 * each segment matches PATH_SEGMENT and is neither "." nor "..".
 */
export function isValidRelativePath(path: string): boolean {
  const p = path.trim().replace(/^\/+|\/+$/g, "");
  if (!p) return true;
  if (p.length > 1024) return false;
  return p
    .split("/")
    .every(
      (segment) =>
        segment !== "." &&
        segment !== ".." &&
        segment.length <= 255 &&
        PATH_SEGMENT.test(segment),
    );
}

/** Size in bytes of a PHP size value ("64M"), or null when malformed. */
export function parsePhpSize(value: string): number | null {
  const match = PHP_SIZE.exec(value.trim());
  if (!match) return null;
  const units = { K: 1024, M: 1024 ** 2, G: 1024 ** 3 } as const;
  return Number(match[1]) * units[match[2] as keyof typeof units];
}

/** UTF-8 size of a text, as counted by the API. */
export function byteLength(text: string): number {
  return new TextEncoder().encode(text).length;
}

/** Directory inside the document root, for Symfony asset dirs (not empty). */
export function isValidAssetDir(dir: string): boolean {
  const d = dir.trim().replace(/^\/+|\/+$/g, "");
  return !!d && isValidRelativePath(d);
}
