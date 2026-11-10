import { CheckCircleOutlined, FolderOutlined } from "@ant-design/icons";
import { Button, Form, Input, Space, Tooltip, Typography } from "antd";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { templateHasPhp } from "../../../Services/Hosting/Models/hosting.model";
import { normalizePath } from "./formPaths";
import { DEFAULT_ASSET_DIRS, HostingFormValues } from "./formValues";
import { FRAMEWORK_LABELS } from "./Routing/frameworks";
import { isValidRelativePath } from "./validators";

/** Common document roots (folder names, not translated). */
const QUICK_ROOTS = ["web", "public"] as const;
type QuickRoot = (typeof QUICK_ROOTS)[number];

/**
 * Document root, with quick picks for the usual folders. "public" (the
 * Symfony / Laravel layout) also turns Symfony routing on for PHP templates,
 * unless custom rules are used; nothing is turned off when switching back.
 */
export default function DocumentRootField() {
  const { t } = useTranslation("hosting");
  const form = Form.useFormInstance<HostingFormValues>();
  const documentRoot = Form.useWatch("documentRoot", form);
  const framework = Form.useWatch("framework", form);
  // "public" turned Symfony on: noticed while both are still in place.
  const [enabledByPublic, setEnabledByPublic] = useState(false);
  const current = normalizePath(documentRoot);
  const showNotice =
    enabledByPublic && framework === "symfony" && current === "public";
  // Forget the "enabled by public" origin as soon as it no longer holds, so
  // a later manual "public" + Symfony does not show the notice.
  if (enabledByPublic && !showNotice) setEnabledByPublic(false);

  const pick = (root: QuickRoot) => {
    const values = form.getFieldsValue(true) as HostingFormValues;
    const enableSymfony =
      root === "public" &&
      templateHasPhp(values.template) &&
      !values.customRouting &&
      // Never overrides a framework already chosen (e.g. Laravel).
      !values.framework;
    form.setFieldsValue({
      documentRoot: root,
      ...(enableSymfony
        ? {
            framework: "symfony" as const,
            assetDirs: values.assetDirs?.length
              ? values.assetDirs
              : DEFAULT_ASSET_DIRS,
          }
        : {}),
    });
    if (enableSymfony) setEnabledByPublic(true);
    // Same handling as a manual change (validation, unsaved state).
    form.validateFields(["documentRoot"]).catch(() => undefined);
  };

  return (
    <Form.Item
      label={t("form.fields.documentRoot.label")}
      htmlFor="documentRoot"
      tooltip={t("form.fields.documentRoot.tooltip")}
      extra={
        showNotice && (
          <Typography.Text type="success">
            <CheckCircleOutlined />{" "}
            {t("form.fields.documentRoot.symfonyEnabled", {
              framework: FRAMEWORK_LABELS.symfony,
            })}
          </Typography.Text>
        )
      }
    >
      <Space.Compact style={{ width: "100%" }}>
        <Form.Item
          name="documentRoot"
          noStyle
          rules={[
            {
              validator: async (_, value?: string) => {
                if (!isValidRelativePath(value ?? "")) {
                  throw new Error(t("form.fields.documentRoot.invalid"));
                }
              },
            },
          ]}
        >
          <Input
            prefix={<FolderOutlined />}
            placeholder={t("form.fields.documentRoot.placeholder")}
            allowClear
            autoComplete="off"
            spellCheck={false}
          />
        </Form.Item>
        {QUICK_ROOTS.map((root) => (
          <Tooltip
            key={root}
            title={t(`form.fields.documentRoot.${root}Tooltip`)}
          >
            <Button
              type={current === root ? "primary" : "default"}
              aria-pressed={current === root}
              onClick={() => pick(root)}
            >
              {root}
            </Button>
          </Tooltip>
        ))}
      </Space.Compact>
    </Form.Item>
  );
}
