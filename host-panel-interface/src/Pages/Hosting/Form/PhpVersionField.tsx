import { Alert, Form, Select, Space } from "antd";
import { useMemo } from "react";
import { useTranslation } from "react-i18next";
import { PhpVersionModel } from "../../../Services/Hosting/Models/hosting.model";
import PhpSupportTags from "../PhpSupportTags";
import { HostingFormValues } from "./formValues";

/** Newest version first ("8.10" > "8.9"). */
function compareVersionsDesc(a: string, b: string): number {
  return b.localeCompare(a, undefined, { numeric: true });
}

/** PHP version, grouped (supported / deprecated) with support date tags. */
export default function PhpVersionField({
  phpVersions,
}: Readonly<{ phpVersions: PhpVersionModel[] }>) {
  const { t } = useTranslation("hosting");
  const form = Form.useFormInstance<HostingFormValues>();
  const phpVersion = Form.useWatch("phpVersion", form);

  const versionOptions = useMemo(() => {
    const sorted = [...phpVersions].sort((a, b) =>
      compareVersionsDesc(a.tag, b.tag),
    );
    // The support tags are rendered in the dropdown (optionRender) and under
    // the field, so the selection box only shows the version.
    const toOption = (v: PhpVersionModel) => ({ value: v.tag, label: v.tag });
    const supported = sorted.filter((v) => !v.deprecated).map(toOption);
    const deprecated = sorted.filter((v) => v.deprecated).map(toOption);
    return [
      supported.length > 0 && {
        label: t("form.fields.phpVersion.supported"),
        options: supported,
      },
      deprecated.length > 0 && {
        label: t("form.fields.phpVersion.deprecated"),
        options: deprecated,
      },
    ].filter((group) => group !== false);
  }, [phpVersions, t]);

  const byTag = useMemo(
    () => new Map(phpVersions.map((v) => [v.tag, v])),
    [phpVersions],
  );
  const selected = phpVersion ? byTag.get(phpVersion) : undefined;
  const isDeprecated = selected?.deprecated ?? false;

  return (
    <>
      <Form.Item
        name="phpVersion"
        label={t("form.fields.phpVersion.label")}
        rules={[
          {
            required: true,
            message: t("form.fields.phpVersion.required"),
          },
        ]}
        extra={selected && <PhpSupportTags version={selected} />}
      >
        <Select
          options={versionOptions}
          style={{ maxWidth: 320 }}
          popupMatchSelectWidth={false}
          optionRender={(option) => {
            const version = byTag.get(String(option.value));
            return (
              <Space size={8}>
                <span style={{ minWidth: 72, display: "inline-block" }}>
                  {option.label}
                </span>
                {version && <PhpSupportTags version={version} />}
              </Space>
            );
          }}
        />
      </Form.Item>
      {isDeprecated && (
        <Alert
          type="warning"
          showIcon
          title={t("form.fields.phpVersion.deprecatedWarning")}
          style={{ marginBottom: 24 }}
        />
      )}
    </>
  );
}
