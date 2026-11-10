import { Form, type FormInstance } from "antd";
import { useTranslation } from "react-i18next";
import { HostingFormValues } from "../formValues";

export interface PanelSummaries {
  routing: string;
  env: string;
  phpIni: string;
  vhost: string;
  phpModules: string;
}

/**
 * Short states of the collapsible panels, from the current form values. The
 * form is passed in: the caller renders the <Form>, it is not inside it.
 */
export default function usePanelSummaries(
  form: FormInstance<HostingFormValues>,
  hasPhp: boolean,
): PanelSummaries {
  const { t } = useTranslation("hosting");
  const preserve = { form, preserve: true };
  const framework = Form.useWatch("framework", preserve);
  const custom = Form.useWatch("customRouting", preserve);
  const env: unknown[] | undefined = Form.useWatch("env", preserve);
  const phpIni: string | undefined = Form.useWatch("phpIni", preserve);
  const vhost: string | undefined = Form.useWatch("vhost", preserve);
  const disabledModules: string[] | null | undefined = Form.useWatch(
    ["php", "disabledModules"],
    preserve,
  );

  const filled = (text?: string) =>
    text?.trim()
      ? t("form.layout.summary.configured")
      : t("form.layout.summary.empty");

  let routing = t("detail.overview.routingDefault");
  if (custom) routing = t("detail.overview.routingCustom");
  else if (framework === "symfony" && hasPhp) {
    routing = t("detail.overview.routingSymfony");
  } else if (framework === "laravel" && hasPhp) {
    routing = t("detail.overview.routingLaravel");
  }

  const disabledCount = disabledModules?.length ?? 0;
  return {
    routing,
    env: t("form.layout.summary.variables", { count: env?.length ?? 0 }),
    phpIni: filled(phpIni),
    vhost: filled(vhost),
    phpModules: disabledCount
      ? t("form.layout.summary.disabledModules", { count: disabledCount })
      : t("form.layout.summary.allEnabled"),
  };
}
