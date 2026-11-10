import { Typography } from "antd";
import { ReactNode } from "react";

const { Title } = Typography;

export function PanelTitle({ children }: { children: ReactNode }) {
  return (
    <Title level={4} style={{ margin: 0 }}>
      {children}
    </Title>
  );
}
