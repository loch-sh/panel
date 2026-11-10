import { Divider } from "antd";
import { useTranslation } from "react-i18next";
import PhpModulesField from "../PhpModulesField";
import PhpSettingsFields from "../PhpSettingsFields";
import PhpVersionField from "../PhpVersionField";
import ResourcesFields from "../ResourcesFields";
import FieldPanels from "./FieldPanels";
import { SectionContentProps } from "./sectionProps";

/**
 * PHP version, limits, settings and modules (PHP templates), then the
 * container resource limits.
 */
export default function RuntimeSection({
  config,
  limits,
  hasPhp,
  errorFields,
  failures,
  summaries,
}: Readonly<SectionContentProps>) {
  const { t } = useTranslation("hosting");

  return (
    <>
      {hasPhp && (
        <>
          <PhpVersionField phpVersions={config.phpVersions ?? []} />
          <PhpSettingsFields />
          <FieldPanels
            errorFields={errorFields}
            failures={failures}
            panels={[
              {
                key: "phpModules",
                title: t("form.fields.phpModules.label"),
                summary: summaries.phpModules,
                content: <PhpModulesField />,
              },
            ]}
          />
          <Divider />
        </>
      )}
      <ResourcesFields limits={limits} />
    </>
  );
}
