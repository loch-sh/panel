import { Col, Form, Input, InputNumber, Row, Typography } from "antd";
import { useTranslation } from "react-i18next";
import { HostingFormValues } from "./formValues";
import { memoryLimitRule } from "./phpSizeRules";

const MAX_EXECUTION_TIME = 3600;

function GroupTitle({ children }: Readonly<{ children: string }>) {
  return (
    <Typography.Title level={5} style={{ marginTop: 0 }}>
      {children}
    </Typography.Title>
  );
}

/**
 * PHP limits in two groups: web requests (PHP-FPM behind Nginx / Apache) and
 * the command line (crons, console), whose memory limit is also the web
 * default.
 */
export default function PhpLimitsFields() {
  const { t } = useTranslation("hosting");
  const form = Form.useFormInstance<HostingFormValues>();
  const cliMemory: string = Form.useWatch(["php", "memoryLimit"], form) ?? "";

  return (
    <Row gutter={24}>
      <Col xs={24} md={12}>
        <GroupTitle>{t("form.fields.phpGroups.web")}</GroupTitle>
        <Form.Item
          name={["php", "webMemoryLimit"]}
          label={t("form.fields.webMemoryLimit.label")}
          tooltip={t("form.fields.webMemoryLimit.tooltip")}
          rules={[memoryLimitRule(t, true)]}
        >
          <Input
            placeholder={t("form.fields.webMemoryLimit.placeholder", {
              value: cliMemory.trim() || "—",
            })}
            autoComplete="off"
            allowClear
          />
        </Form.Item>
        <Form.Item
          name={["php", "maxExecutionTime"]}
          label={t("form.fields.maxExecutionTime.label")}
          tooltip={t("form.fields.maxExecutionTime.tooltip")}
          extra={t("form.fields.maxExecutionTime.help")}
          rules={[
            {
              required: true,
              type: "number",
              min: 0,
              max: MAX_EXECUTION_TIME,
              message: t("form.fields.maxExecutionTime.invalid", {
                max: MAX_EXECUTION_TIME,
              }),
            },
          ]}
        >
          <InputNumber
            min={0}
            max={MAX_EXECUTION_TIME}
            precision={0}
            suffix={t("form.fields.maxExecutionTime.unit")}
            style={{ width: "100%" }}
          />
        </Form.Item>
      </Col>
      <Col xs={24} md={12}>
        <GroupTitle>{t("form.fields.phpGroups.cli")}</GroupTitle>
        <Form.Item
          name={["php", "memoryLimit"]}
          label={t("form.fields.memoryLimit.label")}
          tooltip={t("form.fields.memoryLimit.tooltip")}
          extra={t("form.fields.memoryLimit.help")}
          rules={[memoryLimitRule(t)]}
        >
          <Input placeholder="256M" autoComplete="off" />
        </Form.Item>
      </Col>
    </Row>
  );
}
