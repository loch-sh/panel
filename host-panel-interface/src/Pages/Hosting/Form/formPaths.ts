import { HostingTemplate } from "../../../Services/Hosting/Models/hosting.model";

/**
 * Normalization of the paths typed in the form (same as the payload) and the
 * web server family of a template.
 */

/** Trims a relative path and strips its leading / trailing slashes. */
export function normalizePath(path: string | undefined | null): string {
  return (path ?? "").trim().replace(/^\/+|\/+$/g, "");
}

/** Normalized, non-empty asset directories. */
export function normalizeAssetDirs(
  dirs: string[] | undefined | null,
): string[] {
  return (dirs ?? []).map(normalizePath).filter(Boolean);
}

export type ServerFamily = "Nginx" | "Apache";

/** Web server of a template: the syntax of custom rules depends on it. */
export function familyOf(template?: HostingTemplate): ServerFamily {
  return template === "php-nginx" || template === "nginx" ? "Nginx" : "Apache";
}
