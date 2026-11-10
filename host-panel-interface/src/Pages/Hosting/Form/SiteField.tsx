import { FolderOutlined } from "@ant-design/icons";
import { Form, Input, Radio, Select, Typography } from "antd";
import { useMemo } from "react";
import { useTranslation } from "react-i18next";
import { HostingSiteModel } from "../../../Services/Hosting/Models/hosting.model";
import { HostingFormValues } from "./formValues";

interface Props {
  isEdit: boolean;
  sitesDirectory?: string;
  sites: HostingSiteModel[];
}

/**
 * Site directory: a new directory named after the stack, or an existing one
 * not used by another stack. Read-only in edit mode.
 */
export default function SiteField({
  isEdit,
  sitesDirectory,
  sites,
}: Readonly<Props>) {
  const { t } = useTranslation("hosting");
  const form = Form.useFormInstance<HostingFormValues>();
  const siteMode = Form.useWatch("siteMode", form);
  const name = Form.useWatch("name", form);
  const site = Form.useWatch("site", form);
  const dir = sitesDirectory ?? "";

  const freeSites = useMemo(
    () =>
      sites
        .filter((s) => !s.stack)
        .map((s) => ({ value: s.name, label: s.name })),
    [sites],
  );

  return (
    <Form.Item
      label={t("form.fields.site.label")}
      tooltip={t("form.fields.site.tooltip", { dir })}
      required={!isEdit}
    >
      {isEdit ? (
        <>
          <Input
            prefix={<FolderOutlined />}
            value={site}
            disabled
            aria-label={t("form.fields.site.label")}
          />
          <Typography.Text type="secondary">
            {t("form.fields.site.readOnly")}
          </Typography.Text>
        </>
      ) : (
        <>
          <Form.Item name="siteMode" noStyle>
            <Radio.Group
              optionType="button"
              options={[
                { value: "new", label: t("form.fields.site.new") },
                {
                  value: "existing",
                  label: t("form.fields.site.existing"),
                  disabled: freeSites.length === 0,
                },
              ]}
            />
          </Form.Item>
          <div style={{ marginTop: 8 }}>
            {siteMode === "existing" ? (
              <Form.Item
                name="site"
                noStyle
                rules={[
                  {
                    required: true,
                    message: t("form.fields.site.required"),
                  },
                ]}
              >
                <Select
                  showSearch
                  options={freeSites}
                  placeholder={t("form.fields.site.select")}
                  suffixIcon={<FolderOutlined />}
                  aria-label={t("form.fields.site.select")}
                />
              </Form.Item>
            ) : (
              <Typography.Text type="secondary">
                {t("form.fields.site.newHelp", {
                  path: `${dir}/${name || t("form.fields.name.placeholder")}`,
                })}
              </Typography.Text>
            )}
            {freeSites.length === 0 && (
              <div>
                <Typography.Text type="secondary">
                  {t("form.fields.site.noneAvailable")}
                </Typography.Text>
              </div>
            )}
          </div>
        </>
      )}
    </Form.Item>
  );
}
