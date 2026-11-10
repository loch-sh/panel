import { Col, Row } from "antd";
import { useTranslation } from "react-i18next";
import { HostingTemplate } from "../../../Services/Hosting/Models/hosting.model";
import TemplateCard from "./TemplateCard";

interface Props {
  templates: HostingTemplate[];
  /** templates that can be selected (all when omitted) */
  allowed?: HostingTemplate[];
  value?: HostingTemplate;
  onChange?: (value: HostingTemplate) => void;
}

/** Selectable template cards (form control, behaves like a radio group). */
export default function TemplatePicker({
  templates,
  allowed,
  value,
  onChange,
}: Readonly<Props>) {
  const select = (template: HostingTemplate) => {
    if (allowed && !allowed.includes(template)) return;
    onChange?.(template);
  };

  const { t } = useTranslation("hosting");
  return (
    <Row
      gutter={[16, 16]}
      role="radiogroup"
      aria-label={t("form.layout.type.title")}
    >
      {templates.map((template) => (
        <Col key={template} xs={24} sm={12} xl={6}>
          <TemplateCard
            template={template}
            selected={value === template}
            disabled={!!allowed && !allowed.includes(template)}
            onSelect={() => select(template)}
          />
        </Col>
      ))}
    </Row>
  );
}
