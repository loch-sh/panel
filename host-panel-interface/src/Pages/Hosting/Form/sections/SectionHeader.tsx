import { Flex, Typography, theme } from "antd";
import { ReactNode } from "react";

interface Props {
  icon: ReactNode;
  title: string;
  subtitle: string;
}

/** Card title of a form section: icon, title and a one-line subtitle. */
export default function SectionHeader({
  icon,
  title,
  subtitle,
}: Readonly<Props>) {
  const { token } = theme.useToken();
  return (
    <Flex gap={token.marginSM} align="center" style={{ padding: "4px 0" }}>
      <span
        aria-hidden
        style={{ fontSize: token.fontSizeHeading4, color: token.colorPrimary }}
      >
        {icon}
      </span>
      <Flex vertical>
        <Typography.Text strong>{title}</Typography.Text>
        <Typography.Text
          type="secondary"
          style={{ fontSize: token.fontSizeSM, fontWeight: "normal" }}
        >
          {subtitle}
        </Typography.Text>
      </Flex>
    </Flex>
  );
}
