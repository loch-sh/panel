import { Affix, Card, Flex, Typography, theme } from "antd";
import { ReactNode } from "react";

const { Text } = Typography;

/** A numbered section of a full-page creation form. */
export function NumberedSection({
  step,
  title,
  subtitle,
  children,
}: Readonly<{
  step: number;
  title: string;
  subtitle: string;
  children: ReactNode;
}>) {
  const { token } = theme.useToken();
  return (
    <Card
      title={
        <Flex gap="small" align="center" style={{ padding: "4px 0" }}>
          <span
            aria-hidden
            style={{
              display: "inline-flex",
              width: 28,
              height: 28,
              borderRadius: "50%",
              alignItems: "center",
              justifyContent: "center",
              background: token.colorPrimary,
              color: token.colorTextLightSolid,
              fontWeight: 600,
              flex: "none",
            }}
          >
            {step}
          </span>
          <Flex vertical>
            <Text strong role="heading" aria-level={2}>
              {title}
            </Text>
            <Text
              type="secondary"
              style={{
                fontSize: token.fontSizeSM,
                fontWeight: "normal",
                whiteSpace: "normal",
              }}
            >
              {subtitle}
            </Text>
          </Flex>
        </Flex>
      }
    >
      {children}
    </Card>
  );
}

/**
 * Keeps the summary in view while the form scrolls, on wide screens only
 * (below, the summary follows the form and the action bar is fixed).
 */
export function SummaryAffix({
  enabled,
  offsetTop,
  children,
}: Readonly<{ enabled: boolean; offsetTop: number; children: ReactNode }>) {
  return enabled ? <Affix offsetTop={offsetTop}>{children}</Affix> : children;
}

/** Small screens: the summary is below the form; keeps the action at hand. */
export function BottomActionBar({
  children,
}: Readonly<{ children: ReactNode }>) {
  const { token } = theme.useToken();
  return (
    <Affix offsetBottom={0}>
      <div
        style={{
          padding: token.paddingSM,
          background: token.colorBgContainer,
          borderTop: `1px solid ${token.colorBorderSecondary}`,
        }}
      >
        {children}
      </div>
    </Affix>
  );
}
