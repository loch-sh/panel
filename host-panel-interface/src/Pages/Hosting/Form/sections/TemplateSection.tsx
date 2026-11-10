import { Form } from "antd";
import { useTranslation } from "react-i18next";
import TemplatePicker from "../TemplatePicker";
import { SectionProps } from "./sectionProps";

/** Template of the stack: four selectable cards. */
export default function TemplateSection({
  mode,
  templates,
  allowedTemplates,
}: Readonly<SectionProps>) {
  const { t } = useTranslation("hosting");
  return (
    <Form.Item
      name="template"
      rules={[{ required: true, message: t("form.fields.template.required") }]}
      extra={mode === "edit" ? t("form.fields.template.editHelp") : undefined}
      style={{ marginBottom: 0 }}
    >
      <TemplatePicker templates={templates} allowed={allowedTemplates} />
    </Form.Item>
  );
}
