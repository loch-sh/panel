import { Framework } from "../../../../Services/Hosting/Models/hosting.model";
import { normalizePath } from "../formPaths";

/** Display names of the frameworks (brand names, not translated). */
export const FRAMEWORK_LABELS: Record<Framework, string> = {
  symfony: "Symfony",
  laravel: "Laravel",
};

/** True when the document root is a "public" directory (last segment). */
export function isPublicRoot(documentRoot?: string | null): boolean {
  return normalizePath(documentRoot).split("/").pop() === "public";
}
