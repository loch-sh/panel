import { Col, Form, Row, Select, Switch } from "antd";
import { useTranslation } from "react-i18next";
import { HostingFormValues } from "./formValues";
import { isValidDomain } from "./validators";

interface Props {
  maxDomains: number;
}

export default function DomainsFields({ maxDomains }: Readonly<Props>) {
  const { t } = useTranslation("hosting");
  const form = Form.useFormInstance<HostingFormValues>();
  const https = Form.useWatch("https", form);

  return (
    <>
      <Form.Item
        name="domains"
        label={t("form.fields.domains.label")}
        extra={t("form.fields.domains.help", { max: maxDomains })}
        normalize={(value?: string[]) =>
          (value ?? []).map((d) => d.trim().toLowerCase()).filter(Boolean)
        }
        rules={[
          {
            validator: async (_, value?: string[]) => {
              const domains = value ?? [];
              if (domains.length > maxDomains) {
                throw new Error(
                  t("form.fields.domains.tooMany", { max: maxDomains }),
                );
              }
              const invalid = domains.find((d) => !isValidDomain(d));
              if (invalid) {
                throw new Error(
                  t("form.fields.domains.invalid", { domain: invalid }),
                );
              }
              const duplicate = domains.find(
                (d, i) => domains.indexOf(d) !== i,
              );
              if (duplicate) {
                throw new Error(
                  t("form.fields.domains.duplicate", { domain: duplicate }),
                );
              }
            },
          },
        ]}
      >
        <Select
          mode="tags"
          open={false}
          suffixIcon={null}
          tokenSeparators={[",", " ", ";"]}
          placeholder={t("form.fields.domains.placeholder")}
          aria-label={t("form.fields.domains.label")}
        />
      </Form.Item>

      <Row gutter={16}>
        <Col xs={24} md={12}>
          <Form.Item
            name="https"
            label={t("form.fields.https.label")}
            valuePropName="checked"
            extra={t("form.fields.https.help")}
          >
            <Switch
              onChange={(checked) =>
                !checked && form.setFieldValue("redirectHttps", false)
              }
            />
          </Form.Item>
        </Col>
        <Col xs={24} md={12}>
          <Form.Item
            name="redirectHttps"
            label={t("form.fields.redirectHttps.label")}
            valuePropName="checked"
            extra={t("form.fields.redirectHttps.help")}
          >
            <Switch disabled={!https} />
          </Form.Item>
        </Col>
      </Row>
    </>
  );
}
