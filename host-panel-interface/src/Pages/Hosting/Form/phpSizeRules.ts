import type { TFunction } from "i18next";
import { parsePhpSize } from "./validators";

/** A PHP memory limit: a size such as 256M, or -1 for unlimited. */
export function isValidMemoryLimit(value: string): boolean {
  const v = value.trim();
  return v === "-1" || parsePhpSize(v) !== null;
}

/**
 * Form rule of a memory limit field. `optional`: "" is allowed (web limit,
 * "" = same as the CLI limit).
 */
export function memoryLimitRule(t: TFunction<"hosting">, optional = false) {
  return {
    validator: async (_: unknown, value?: string) => {
      const v = (value ?? "").trim();
      if (optional && !v) return;
      if (!isValidMemoryLimit(v)) {
        throw new Error(t("form.fields.memoryLimit.invalid"));
      }
    },
  };
}

/** Form rule of an upload size field (upload_max_filesize, post_max_size). */
export function sizeRule(
  t: TFunction<"hosting">,
  messageKey: "uploadMaxFilesize" | "postMaxSize",
) {
  return {
    validator: async (_: unknown, value?: string) => {
      if (parsePhpSize(value ?? "") === null) {
        throw new Error(t(`form.fields.${messageKey}.invalid`));
      }
    },
  };
}
