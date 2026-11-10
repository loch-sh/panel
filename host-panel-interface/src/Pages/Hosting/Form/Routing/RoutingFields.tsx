import { Form, Switch } from "antd";
import { useTranslation } from "react-i18next";
import { templateHasPhp } from "../../../../Services/Hosting/Models/hosting.model";
import { familyOf } from "../formPaths";
import { HostingFormValues } from "../formValues";
import AssetDirsField from "./AssetDirsField";
import CustomRulesField from "./CustomRulesField";
import FrameworkField from "./FrameworkField";
import { isPublicRoot } from "./frameworks";
import useFamilyChangeWarning from "./useFamilyChangeWarning";
import usePrefillRoutingRules from "./usePrefillRoutingRules";

interface Props {
  maxBytes: number;
  /** a prefill request is in flight (the form disables its submit button) */
  onPrefillingChange?: (prefilling: boolean) => void;
}

/**
 * Routing of the web server: image default, a framework front controller
 * (Symfony / Laravel, PHP templates) or custom rules replacing the routing of
 * the image.
 */
export default function RoutingFields({
  maxBytes,
  onPrefillingChange,
}: Readonly<Props>) {
  const { t } = useTranslation("hosting");
  const form = Form.useFormInstance<HostingFormValues>();
  const template = Form.useWatch("template", form);
  const framework: HostingFormValues["framework"] =
    Form.useWatch("framework", form) ?? "";
  const custom = !!Form.useWatch("customRouting", form);
  const rules: string = Form.useWatch("routingRules", form) ?? "";
  const documentRoot = Form.useWatch("documentRoot", form);
  const hasPhp = templateHasPhp(template);
  const family = familyOf(template);
  const symfonyOn = hasPhp && framework === "symfony";

  const { rulesFamily, setRulesFamily, showWarning } = useFamilyChangeWarning(
    family,
    familyOf(form.getFieldValue("template")),
    rules,
  );
  const { prefilling, prefill, cancel } = usePrefillRoutingRules(form, {
    symfonyOn,
    onPrefillingChange,
    onPrefilled: setRulesFamily,
  });

  const onCustomChange = (checked: boolean) => {
    if (!checked) {
      cancel();
      return;
    }
    setRulesFamily(family);
    if (hasPhp && !rules.trim()) void prefill();
  };

  return (
    <>
      <FrameworkField
        hasPhp={hasPhp}
        family={family}
        custom={custom}
        framework={hasPhp ? framework : ""}
        outsidePublic={!isPublicRoot(documentRoot)}
      />
      {symfonyOn && <AssetDirsField disabled={custom} />}

      <Form.Item
        name="customRouting"
        label={t("form.fields.routing.custom.label")}
        valuePropName="checked"
        extra={
          family === "Nginx"
            ? t("form.fields.routing.custom.helpNginx")
            : t("form.fields.routing.custom.helpApache")
        }
      >
        <Switch onChange={onCustomChange} />
      </Form.Item>

      {custom && (
        <CustomRulesField
          rules={rules}
          maxBytes={maxBytes}
          canPrefill={hasPhp}
          prefilling={prefilling}
          onReset={() => void prefill()}
          familyWarning={
            showWarning ? { from: rulesFamily, to: family } : undefined
          }
        />
      )}
    </>
  );
}
