import { DeleteOutlined, PlusOutlined } from "@ant-design/icons";
import { Button, Col, Form, Input, Row, Tooltip, Typography } from "antd";
import { useTranslation } from "react-i18next";
import { EnvVar } from "../../../Services/Hosting/Models/hosting.model";
import { isReservedEnvName, isValidEnvName } from "./validators";

interface Props {
  maxEnvVars: number;
}

/** Free environment variables of the web service (name / value list). */
export default function EnvVarsField({ maxEnvVars }: Readonly<Props>) {
  const { t } = useTranslation("hosting");

  return (
    <Form.Item
      label={t("form.fields.env.label")}
      extra={t("form.fields.env.help")}
    >
      <Form.List
        name="env"
        rules={[
          {
            validator: async (_, value?: EnvVar[]) => {
              if ((value ?? []).length > maxEnvVars) {
                throw new Error(
                  t("form.fields.env.tooMany", { max: maxEnvVars }),
                );
              }
            },
          },
        ]}
      >
        {(fields, { add, remove }, { errors }) => (
          <>
            {fields.map((field) => (
              <Row key={field.key} gutter={8} wrap={false} align="top">
                <Col flex="2">
                  <Form.Item
                    name={[field.name, "name"]}
                    validateTrigger={["onChange", "onBlur"]}
                    // The duplicate check depends on the other names.
                    dependencies={fields
                      .filter((other) => other.key !== field.key)
                      .map((other) => ["env", other.name, "name"])}
                    rules={[
                      {
                        required: true,
                        whitespace: true,
                        message: t("form.fields.env.nameRequired"),
                      },
                      ({ getFieldValue }) => ({
                        validator: async (_, value?: string) => {
                          const name = (value ?? "").trim();
                          if (!name) return;
                          if (!isValidEnvName(name)) {
                            throw new Error(t("form.fields.env.nameInvalid"));
                          }
                          if (isReservedEnvName(name)) {
                            throw new Error(t("form.fields.env.nameReserved"));
                          }
                          const all: EnvVar[] = getFieldValue("env") ?? [];
                          const count = all.filter(
                            (v) => (v?.name ?? "").trim() === name,
                          ).length;
                          if (count > 1) {
                            throw new Error(t("form.fields.env.nameDuplicate"));
                          }
                        },
                      }),
                    ]}
                  >
                    <Input
                      placeholder={t("form.fields.env.namePlaceholder")}
                      aria-label={t("form.fields.env.namePlaceholder")}
                      autoComplete="off"
                      spellCheck={false}
                      style={{ fontFamily: "monospace" }}
                    />
                  </Form.Item>
                </Col>
                <Col flex="3">
                  <Form.Item
                    name={[field.name, "value"]}
                    rules={[
                      {
                        validator: async (_, value?: string) => {
                          if (/[\r\n\0]/.test(value ?? "")) {
                            throw new Error(t("form.fields.env.valueInvalid"));
                          }
                        },
                      },
                    ]}
                  >
                    <Input
                      placeholder={t("form.fields.env.valuePlaceholder")}
                      aria-label={t("form.fields.env.valuePlaceholder")}
                      autoComplete="off"
                      spellCheck={false}
                      style={{ fontFamily: "monospace" }}
                    />
                  </Form.Item>
                </Col>
                <Col flex="none">
                  <Tooltip title={t("form.fields.env.remove")}>
                    <Button
                      type="text"
                      danger
                      icon={<DeleteOutlined />}
                      aria-label={t("form.fields.env.remove")}
                      onClick={() => remove(field.name)}
                    />
                  </Tooltip>
                </Col>
              </Row>
            ))}
            <Button
              type="dashed"
              icon={<PlusOutlined />}
              onClick={() => add({ name: "", value: "" })}
              disabled={fields.length >= maxEnvVars}
            >
              {t("form.fields.env.add")}
            </Button>
            <Typography.Text type="danger">
              <Form.ErrorList errors={errors} />
            </Typography.Text>
          </>
        )}
      </Form.List>
    </Form.Item>
  );
}
