import { Card, Flex, Spin, Typography } from "antd";
import { useMemo } from "react";
import { useTranslation } from "react-i18next";
import StackStats from "../../../../Components/StackStats/StackStats";
import useStackStats from "../../../../Services/Containers/Hooks/useStackStats";
import { HostingContainer } from "../../../../Services/Hosting/Models/hosting.model";

const { Text } = Typography;

/** Live memory / CPU usage of the running containers of the stack. */
export default function UsageCard({
  containers,
}: Readonly<{ containers?: HostingContainer[] }>) {
  const { t } = useTranslation("hosting");
  // Live memory/CPU usage, always on (unlike the Containers page toggle).
  const runningIds = useMemo(
    () =>
      (containers ?? []).filter((c) => c.state === "running").map((c) => c.id),
    [containers],
  );
  const stats = useStackStats(runningIds, true);

  return (
    <Card size="small" title={t("detail.overview.usage")}>
      {runningIds.length > 0 && stats.current.length === 0 ? (
        // Containers are running but the first measures are not in yet.
        <Flex
          justify="center"
          align="center"
          gap="small"
          style={{ padding: 24 }}
        >
          <Spin size="small" />
          <Text type="secondary">{t("detail.overview.usageLoading")}</Text>
        </Flex>
      ) : runningIds.length > 0 ? (
        <StackStats snapshot={stats} />
      ) : (
        <Text type="secondary">{t("detail.overview.usageNotRunning")}</Text>
      )}
    </Card>
  );
}
