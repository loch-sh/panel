import { WarningOutlined } from "@ant-design/icons";
import { Space, Tag, Tooltip } from "antd";
import { useTranslation } from "react-i18next";

interface Props {
  version?: string;
  deprecated: boolean;
}

export default function PhpVersionTag({
  version,
  deprecated,
}: Readonly<Props>) {
  const { t } = useTranslation("hosting");
  if (!version) return <>—</>;
  return (
    <Space size={4}>
      <Tag>{version}</Tag>
      {deprecated && (
        <Tooltip title={t("php.deprecatedTooltip")}>
          <Tag color="warning" icon={<WarningOutlined />}>
            {t("php.deprecated")}
          </Tag>
        </Tooltip>
      )}
    </Space>
  );
}
