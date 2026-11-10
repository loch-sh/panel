import { FormInstance } from "antd";
import { useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { toast } from "react-toastify";
import { hostingService } from "../../../../Services/Hosting/hosting.service";
import { Framework } from "../../../../Services/Hosting/Models/hosting.model";
import { getApiErrorMessage } from "../../../../Utils/apiErrors";
import {
  ServerFamily,
  familyOf,
  normalizeAssetDirs,
  normalizePath,
} from "../formPaths";
import { DEFAULT_ASSET_DIRS, HostingFormValues } from "../formValues";
import { isValidAssetDir } from "../validators";

/** Inputs of the generated rules, to detect stale responses. */
interface PrefillInputs {
  template: HostingFormValues["template"];
  framework: Framework;
  documentRoot: string;
  assetDirs: string[];
  maxExecutionTime: number;
}

function sameInputs(a: PrefillInputs, b: PrefillInputs): boolean {
  return (
    a.template === b.template &&
    a.framework === b.framework &&
    a.documentRoot === b.documentRoot &&
    a.assetDirs.join(",") === b.assetDirs.join(",") &&
    a.maxExecutionTime === b.maxExecutionTime
  );
}

/** Inputs of the generated rules from the form, normalized like the payload. */
function currentInputs(form: FormInstance<HostingFormValues>): PrefillInputs {
  const values = form.getFieldsValue(true) as HostingFormValues;
  const dirs = normalizeAssetDirs(values.assetDirs).filter(isValidAssetDir);
  return {
    template: values.template,
    // Symfony when no framework is selected.
    framework: values.framework || "symfony",
    documentRoot: normalizePath(values.documentRoot),
    // Without Symfony the asset dirs field is hidden: reuse the dirs set
    // earlier, or the defaults (ignored by the API for Laravel).
    assetDirs: dirs.length ? dirs : DEFAULT_ASSET_DIRS,
    maxExecutionTime: values.php?.maxExecutionTime ?? 0,
  };
}

interface Options {
  /** validate the asset dirs too before fetching (Symfony selected) */
  symfonyOn: boolean;
  /** a request is in flight (the form disables its submit button) */
  onPrefillingChange?: (prefilling: boolean) => void;
  /** the rules were written, for the given web server family */
  onPrefilled: (family: ServerFamily) => void;
}

/**
 * Fills the custom routing rules with the framework rules the server would
 * generate (GET /hosting/routing-template). Only the latest request may write
 * the rules, and only if its inputs still match the form; unmounting or
 * `cancel()` drops a pending request.
 */
export default function usePrefillRoutingRules(
  form: FormInstance<HostingFormValues>,
  { symfonyOn, onPrefillingChange, onPrefilled }: Options,
) {
  const { t } = useTranslation("hosting");
  const [prefilling, setPrefilling] = useState(false);
  const requestId = useRef(0);

  const setBusy = (busy: boolean) => {
    setPrefilling(busy);
    onPrefillingChange?.(busy);
  };

  useEffect(
    () => () => {
      requestId.current++;
      onPrefillingChange?.(false);
    },
    [onPrefillingChange],
  );

  const prefill = async () => {
    try {
      await form.validateFields(
        symfonyOn ? ["documentRoot", "assetDirs"] : ["documentRoot"],
      );
    } catch {
      // The errors are on the fields (the document root may be in another
      // step): say why nothing was generated.
      toast.warning(t("form.fields.routing.rules.prefillInvalid"));
      return;
    }
    const id = ++requestId.current;
    const inputs = currentInputs(form);
    setBusy(true);
    try {
      const generated = await hostingService.routingTemplate(inputs);
      // Ignore a stale response (newer request, or inputs changed meanwhile).
      if (
        id !== requestId.current ||
        !sameInputs(inputs, currentInputs(form))
      ) {
        return;
      }
      form.setFieldValue("routingRules", generated);
      form.validateFields(["routingRules"]).catch(() => undefined);
      onPrefilled(familyOf(inputs.template));
    } catch (err) {
      if (id === requestId.current) toast.error(getApiErrorMessage(err));
    } finally {
      if (id === requestId.current) setBusy(false);
    }
  };

  const cancel = () => {
    requestId.current++;
    setBusy(false);
  };

  return { prefilling, prefill, cancel };
}
