import { Alert, Form, Segmented } from "antd";
import { useTranslation } from "react-i18next";
import { Framework } from "../../../../Services/Hosting/Models/hosting.model";
import { ServerFamily } from "../formPaths";
import { FRAMEWORK_LABELS } from "./frameworks";

interface Props {
  hasPhp: boolean;
  family: ServerFamily;
  /** custom rules on: the choice is kept but inactive */
  custom: boolean;
  /** the document root is not a "public" directory */
  outsidePublic: boolean;
  framework: "" | Framework;
}

/** Framework whose routing the API generates (PHP templates only). */
export default function FrameworkField({
  hasPhp,
  family,
  custom,
  outsidePublic,
  framework,
}: Readonly<Props>) {
  const { t } = useTranslation("hosting");
  const label = t("form.fields.routing.framework.label");
  const options = [
    { value: "", label: t("form.fields.routing.framework.none") },
    { value: "symfony", label: FRAMEWORK_LABELS.symfony },
    { value: "laravel", label: FRAMEWORK_LABELS.laravel },
  ];

  if (!hasPhp) {
    return (
      <Form.Item
        label={label}
        extra={t("form.fields.routing.framework.phpOnly")}
      >
        <Segmented options={options} value="" disabled aria-label={label} />
      </Form.Item>
    );
  }

  const nginx = family === "Nginx";
  let help = t("form.fields.routing.framework.noneHelp");
  if (custom) help = t("form.fields.routing.framework.overriddenByCustom");
  else if (framework === "symfony") {
    help = nginx
      ? t("form.fields.routing.symfony.helpNginx")
      : t("form.fields.routing.symfony.helpApache");
  } else if (framework === "laravel") {
    help = nginx
      ? t("form.fields.routing.laravel.helpNginx")
      : t("form.fields.routing.laravel.helpApache");
  }

  return (
    <>
      <Form.Item name="framework" label={label} extra={help}>
        <Segmented options={options} disabled={custom} aria-label={label} />
      </Form.Item>
      {framework && !custom && outsidePublic && (
        <Alert
          type="warning"
          showIcon
          title={t("form.fields.routing.framework.siteRootWarning", {
            framework: FRAMEWORK_LABELS[framework],
          })}
          style={{ marginBottom: 16 }}
        />
      )}
    </>
  );
}
