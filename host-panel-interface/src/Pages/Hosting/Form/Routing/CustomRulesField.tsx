import { ReloadOutlined } from "@ant-design/icons";
import { Alert, Button, Flex, Form, Input, Popconfirm } from "antd";
import { useTranslation } from "react-i18next";
import ByteCounter from "../ByteCounter";
import { ServerFamily } from "../formPaths";
import { byteLength } from "../validators";

const MONOSPACE = { fontFamily: "monospace", fontSize: 12 } as const;

interface Props {
  rules: string;
  maxBytes: number;
  /** PHP template: the framework rules can be generated */
  canPrefill: boolean;
  prefilling: boolean;
  onReset: () => void;
  /** set when the template switched web server with unchanged rules */
  familyWarning?: { from: ServerFamily; to: ServerFamily };
}

/** Textarea of the custom routing rules, with reset and warnings. */
export default function CustomRulesField({
  rules,
  maxBytes,
  canPrefill,
  prefilling,
  onReset,
  familyWarning,
}: Readonly<Props>) {
  const { t } = useTranslation("hosting");

  const resetButton = (onClick?: () => void) => (
    <Button
      size="small"
      icon={<ReloadOutlined />}
      loading={prefilling}
      onClick={onClick}
    >
      {t("form.fields.routing.rules.reset")}
    </Button>
  );

  return (
    <>
      {familyWarning && (
        <Alert
          type="warning"
          showIcon
          title={t("form.fields.routing.familyWarning", familyWarning)}
          style={{ marginBottom: 16 }}
        />
      )}
      {canPrefill ? (
        <Flex justify="flex-end" style={{ marginBottom: 8 }}>
          {rules.trim() ? (
            <Popconfirm
              title={t("form.fields.routing.rules.resetConfirm")}
              okText={t("form.fields.routing.rules.reset")}
              cancelText={t("actions.cancel", { ns: "common" })}
              onConfirm={onReset}
            >
              {resetButton()}
            </Popconfirm>
          ) : (
            resetButton(onReset)
          )}
        </Flex>
      ) : (
        <Alert
          type="info"
          showIcon
          title={t("form.fields.routing.custom.noPrefill")}
          style={{ marginBottom: 16 }}
        />
      )}
      <Form.Item
        label={t("form.fields.routing.rules.label")}
        htmlFor="routingRules"
        required
      >
        <Form.Item
          name="routingRules"
          noStyle
          rules={[
            {
              required: true,
              whitespace: true,
              message: t("form.fields.routing.rules.required"),
            },
            {
              validator: async (_, value?: string) => {
                if (byteLength(value ?? "") > maxBytes) {
                  throw new Error(
                    t("form.fields.snippetTooLarge", { max: maxBytes }),
                  );
                }
              },
            },
          ]}
        >
          <Input.TextArea
            rows={12}
            spellCheck={false}
            autoComplete="off"
            disabled={prefilling}
            style={MONOSPACE}
          />
        </Form.Item>
        <ByteCounter text={rules} max={maxBytes} />
      </Form.Item>
    </>
  );
}
