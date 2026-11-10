import type { FormInstance } from "antd";
import { isAxiosError } from "axios";
import i18n from "../../../i18n";
import { templateHasPhp } from "../../../Services/Hosting/Models/hosting.model";
import { getApiErrorMessage } from "../../../Utils/apiErrors";
import { familyOf } from "./formPaths";
import { HostingFormValues } from "./formValues";

/**
 * API validation errors of the stack form: message selection and placement
 * on the matching form item (via the `field` of the error body).
 */

/** Form item path of each `field` value returned by the API. */
function fieldPath(
  field: string,
  values: Partial<HostingFormValues>,
): (string | number)[] | null {
  switch (field) {
    case "name":
    case "template":
    case "phpVersion":
    case "documentRoot":
    case "domains":
    case "redirectHttps":
    case "networks":
    case "env":
    case "phpIni":
    case "vhost":
      return [field];
    // Routing inputs are conditional: null when not mounted (toast instead).
    case "assetDirs":
      return values.framework === "symfony" && templateHasPhp(values.template)
        ? [field]
        : null;
    case "routingRules":
      return values.customRouting ? [field] : null;
    case "routing":
      // The framework choice only exists for PHP templates.
      return values.customRouting || !templateHasPhp(values.template)
        ? ["customRouting"]
        : ["framework"];
    case "site":
      return values.siteMode === "new" ? ["name"] : ["site"];
    // Resource inputs may be hidden (no limit): shown as a toast instead.
    case "resources":
      return null;
    case "php":
      return ["php", "memoryLimit"];
    default:
      return field.startsWith("php.") ? ["php", field.slice(4)] : null;
  }
}

interface ApiErrorBody {
  code?: string;
  error?: string;
  field?: string;
  reason?: string;
}

/**
 * Message of a hosting API error. INVALID_ROUTING carries a precise server
 * message (e.g. the offending directive), shown as is; rules rejected after a
 * web server change get a translated explanation.
 */
export function hostingErrorMessage(
  err: unknown,
  values?: Partial<HostingFormValues>,
): string {
  if (isAxiosError(err)) {
    const data = err.response?.data as ApiErrorBody | undefined;
    if (data?.code === "INVALID_ROUTING") {
      if (data.reason === "web_server_changed") {
        return i18n.t("hosting:form.fields.routing.rulesWrongFamily", {
          family: familyOf(values?.template),
        });
      }
      if (data.error) return data.error;
    }
  }
  return getApiErrorMessage(err);
}

/**
 * Puts an API validation error on the matching form item. Returns false when
 * the error has no field the form knows (the caller then shows a toast).
 */
export function applyServerFieldError(
  form: FormInstance<HostingFormValues>,
  err: unknown,
): boolean {
  if (!isAxiosError(err)) return false;
  const field = (err.response?.data as { field?: unknown } | undefined)?.field;
  if (typeof field !== "string") return false;
  const values = form.getFieldsValue(true) as HostingFormValues;
  const path = fieldPath(field, values);
  if (!path) return false;
  // Paths come from the switch above, all of them are fields of the form.
  const name = path as Parameters<typeof form.scrollToField>[0];
  form.setFields([{ name, errors: [hostingErrorMessage(err, values)] }]);
  form.scrollToField(name, { block: "center" });
  return true;
}
