import { Col, Divider, Form, Input, Row, Select, Switch } from "antd";
import { useMemo } from "react";
import { useTranslation } from "react-i18next";
import { HostingFormValues } from "./formValues";
import PhpLimitsFields from "./PhpLimitsFields";
import { sizeRule } from "./phpSizeRules";
import { listTimezones } from "./timezones";
import { parsePhpSize } from "./validators";

/** PHP settings: limits, upload sizes, time zone and error display. */
export default function PhpSettingsFields() {
  const { t } = useTranslation("hosting");
  const form = Form.useFormInstance<HostingFormValues>();
  const timezone = Form.useWatch(["php", "timezone"], form);
  const zones = useMemo(listTimezones, []);
  const timezoneOptions = useMemo(
    // Keep a saved time zone the browser does not know selectable.
    () =>
      (timezone && !zones.includes(timezone)
        ? [timezone, ...zones]
        : zones
      ).map((z) => ({ value: z, label: z })),
    [zones, timezone],
  );

  return (
    <>
      <PhpLimitsFields />
      <Divider style={{ marginTop: 0 }} />
      <Row gutter={16}>
        <Col xs={24} md={8}>
          <Form.Item
            name={["php", "uploadMaxFilesize"]}
            label={t("form.fields.uploadMaxFilesize.label")}
            tooltip={t("form.fields.uploadMaxFilesize.tooltip")}
            rules={[sizeRule(t, "uploadMaxFilesize")]}
          >
            <Input placeholder="64M" autoComplete="off" />
          </Form.Item>
        </Col>
        <Col xs={24} md={8}>
          <Form.Item
            name={["php", "postMaxSize"]}
            label={t("form.fields.postMaxSize.label")}
            tooltip={t("form.fields.postMaxSize.tooltip")}
            dependencies={[["php", "uploadMaxFilesize"]]}
            rules={[
              sizeRule(t, "postMaxSize"),
              ({ getFieldValue }) => ({
                validator: async (_, value?: string) => {
                  const post = parsePhpSize(value ?? "");
                  const upload = parsePhpSize(
                    getFieldValue(["php", "uploadMaxFilesize"]) ?? "",
                  );
                  if (post !== null && upload !== null && post < upload) {
                    throw new Error(t("form.fields.postMaxSize.tooSmall"));
                  }
                },
              }),
            ]}
          >
            <Input placeholder="64M" autoComplete="off" />
          </Form.Item>
        </Col>
        <Col xs={24} md={8}>
          <Form.Item
            name={["php", "timezone"]}
            label={t("form.fields.timezone.label")}
            rules={[
              { required: true, message: t("form.fields.timezone.required") },
            ]}
          >
            <Select showSearch options={timezoneOptions} />
          </Form.Item>
        </Col>
        <Col xs={24} md={8}>
          <Form.Item
            name={["php", "displayErrors"]}
            label={t("form.fields.displayErrors.label")}
            tooltip={t("form.fields.displayErrors.tooltip")}
            valuePropName="checked"
          >
            <Switch
              checkedChildren={t("actions.yes", { ns: "common" })}
              unCheckedChildren={t("actions.no", { ns: "common" })}
            />
          </Form.Item>
        </Col>
      </Row>
    </>
  );
}
