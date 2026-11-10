import type { FormInstance } from "antd";
import { HostingFormValues } from "./formValues";

/** Structural equality of two JSON-like values (payloads). */
export function deepEqual(a: unknown, b: unknown): boolean {
  if (a === b) return true;
  if (typeof a !== "object" || typeof b !== "object" || !a || !b) {
    return false;
  }
  if (Array.isArray(a) !== Array.isArray(b)) return false;
  const ka = Object.keys(a).filter((k) => (a as never)[k] !== undefined);
  const kb = Object.keys(b).filter((k) => (b as never)[k] !== undefined);
  if (ka.length !== kb.length) return false;
  return ka.every((k) => deepEqual((a as never)[k], (b as never)[k]));
}

/** Same list of field names (order included). */
export function sameNames(
  a: (string | number)[][],
  b: (string | number)[][],
): boolean {
  return (
    a.length === b.length && a.every((n, i) => n.join(".") === b[i].join("."))
  );
}

/**
 * Delay before scrolling to an error: lets a step or a collapsed panel
 * holding the field render and open first.
 */
const SCROLL_DELAY_MS = 250;

/**
 * Scrolls the first field in error (among `names`, all when omitted) into
 * view, once the step / panel holding it is shown. Returns a cancel function.
 */
export function scrollToFirstError(
  form: FormInstance<HostingFormValues>,
  names?: Parameters<FormInstance<HostingFormValues>["getFieldsError"]>[0],
): () => void {
  const timer = window.setTimeout(() => {
    const first = form.getFieldsError(names).find((f) => f.errors.length > 0);
    if (first) {
      form.scrollToField(
        first.name as Parameters<typeof form.scrollToField>[0],
        { block: "center" },
      );
    }
  }, SCROLL_DELAY_MS);
  return () => window.clearTimeout(timer);
}
