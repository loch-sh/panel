import { Tag } from "antd";
import { CSSProperties } from "react";
import { useTranslation } from "react-i18next";

interface StackStatusTagProps {
  runningCount: number;
  healthyCount: number;
  totalCount: number;
  style?: CSSProperties;
}

export default function StackStatusTag({
  runningCount,
  healthyCount,
  totalCount,
  style,
}: StackStatusTagProps) {
  const { t } = useTranslation("containers");
  return (
    <Tag
      color={healthyCount === totalCount ? "green" : "orange"}
      style={style}
    >
      {t("stackStatus.active", { running: runningCount, total: totalCount })}
    </Tag>
  );
}
