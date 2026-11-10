import { Tag } from "antd";
import { useTranslation } from "react-i18next";
import { HostingTemplate } from "../../Services/Hosting/Models/hosting.model";
import { templateColor, templateIcon } from "./templateMeta";

export default function TemplateTag({
  template,
}: Readonly<{ template: HostingTemplate }>) {
  const { t } = useTranslation("hosting");
  return (
    <Tag color={templateColor(template)} icon={templateIcon(template)}>
      {t(`templates.${template}.label`)}
    </Tag>
  );
}
