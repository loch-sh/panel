import { LockOutlined } from "@ant-design/icons";
import { Flex, Tooltip, Typography } from "antd";
import { useTranslation } from "react-i18next";

const { Link, Text } = Typography;

interface Props {
  domains: string[];
  https: boolean;
  vertical?: boolean;
}

export default function DomainLinks({
  domains,
  https,
  vertical,
}: Readonly<Props>) {
  const { t } = useTranslation("hosting");
  if (domains.length === 0) {
    return <Text type="secondary">{t("domains.none")}</Text>;
  }
  const scheme = https ? "https" : "http";
  return (
    <Flex vertical={vertical} gap={vertical ? 2 : 8} wrap>
      {domains.map((domain) => (
        <Flex key={domain} gap={4} align="center">
          {https && (
            <Tooltip title={t("domains.httpsTooltip")}>
              <LockOutlined
                style={{ color: "#52c41a" }}
                aria-label={t("domains.httpsTooltip")}
              />
            </Tooltip>
          )}
          <Link href={`${scheme}://${domain}`} target="_blank" rel="noreferrer">
            {domain}
          </Link>
        </Flex>
      ))}
    </Flex>
  );
}
