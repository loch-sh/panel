import type { TFunction } from "i18next";
import { byteLength } from "./validators";

/** Monospace style of the configuration textareas. */
export const MONOSPACE = { fontFamily: "monospace", fontSize: 12 } as const;

/** Form rule limiting a snippet to the server maximum (UTF-8 bytes). */
export function snippetSizeRule(t: TFunction<"hosting">, maxBytes: number) {
  return {
    validator: async (_: unknown, value?: string) => {
      if (byteLength(value ?? "") > maxBytes) {
        throw new Error(t("form.fields.snippetTooLarge", { max: maxBytes }));
      }
    },
  };
}
