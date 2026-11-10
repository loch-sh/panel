import { Form, Input } from "antd";
import { useTranslation } from "react-i18next";
import ByteCounter from "./ByteCounter";
import { HostingFormValues } from "./formValues";
import { MONOSPACE, snippetSizeRule } from "./snippetRules";

/** Extra php.ini loaded last (PHP templates only). */
export default function PhpIniField({
  maxBytes,
}: Readonly<{ maxBytes: number }>) {
  const { t } = useTranslation("hosting");
  const form = Form.useFormInstance<HostingFormValues>();
  const phpIni = Form.useWatch("phpIni", form);
  const sizeRule = snippetSizeRule(t, maxBytes);

  return (
    // The byte counter sits right under the textarea: the field is a noStyle
    // child so the counter can follow it inside the item.
    <Form.Item
      label={t("form.fields.phpIni.label")}
      htmlFor="phpIni"
      tooltip={t("form.fields.phpIni.tooltip")}
    >
      <Form.Item name="phpIni" noStyle rules={[sizeRule]}>
        <Input.TextArea
          rows={5}
          placeholder={t("form.fields.phpIni.placeholder")}
          spellCheck={false}
          autoComplete="off"
          style={MONOSPACE}
        />
      </Form.Item>
      <ByteCounter text={phpIni} max={maxBytes} />
    </Form.Item>
  );
}
