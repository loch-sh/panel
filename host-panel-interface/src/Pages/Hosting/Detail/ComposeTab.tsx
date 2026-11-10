import { Alert, Card, Flex, Typography, theme } from "antd";
import { memo } from "react";
import { useTranslation } from "react-i18next";
import { HostingStackDetail } from "../../../Services/Hosting/Models/hosting.model";

/**
 * The compose.yaml on disk, as `docker compose` runs it: the hosting can be
 * booted with it without the panel.
 */
function ComposeTab({ stack }: Readonly<{ stack: HostingStackDetail }>) {
  const { t } = useTranslation("hosting");
  const { token } = theme.useToken();

  if (stack.secretsMasked || !stack.compose) {
    return <Alert type="info" showIcon title={t("detail.compose.masked")} />;
  }

  return (
    <Flex vertical gap="middle">
      <Card size="small">
        <Flex vertical gap="middle">
          <Typography.Paragraph type="secondary" style={{ margin: 0 }}>
            {t("detail.compose.help")}
          </Typography.Paragraph>
          {stack.modified && (
            <Alert
              type="warning"
              showIcon
              title={t("detail.compose.modified")}
            />
          )}
          <div style={{ position: "relative" }}>
            <Typography.Text
              copyable={{
                text: stack.compose,
                tooltips: [
                  t("detail.compose.copy"),
                  t("detail.compose.copied"),
                ],
              }}
              style={{ position: "absolute", top: 8, right: 12 }}
            />
            <pre
              aria-label={t("detail.tabs.compose")}
              style={{
                margin: 0,
                padding: 12,
                // Fills the page below the header, tabs and help text.
                height: "max(500px, calc(100vh - 340px))",
                overflow: "auto",
                fontFamily: token.fontFamilyCode,
                fontSize: 12,
                background: token.colorFillTertiary,
                borderRadius: token.borderRadius,
              }}
            >
              {stack.compose}
            </pre>
          </div>
        </Flex>
      </Card>
      {stack.routingFile && (
        <Card size="small" title={t("detail.compose.routingFile")}>
          <Flex vertical gap="small">
            <Typography.Paragraph type="secondary" style={{ margin: 0 }}>
              {t("detail.compose.routingFileHelp")}
            </Typography.Paragraph>
            <pre
              aria-label={t("detail.compose.routingFile")}
              style={{
                margin: 0,
                padding: 12,
                maxHeight: 400,
                overflow: "auto",
                fontFamily: token.fontFamilyCode,
                fontSize: 12,
                background: token.colorFillTertiary,
                borderRadius: token.borderRadius,
              }}
            >
              {stack.routingFile}
            </pre>
          </Flex>
        </Card>
      )}
    </Flex>
  );
}

export default memo(ComposeTab);
