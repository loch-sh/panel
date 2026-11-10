import { BarChartOutlined, CloseOutlined } from "@ant-design/icons";
import { Button } from "antd";
import { memo, useMemo, useState } from "react";
import type { ContainerInfo } from "dockerode";
import { useTranslation } from "react-i18next";
import useStackStats from "../../Services/Containers/Hooks/useStackStats";
import StackStats from "./StackStats";

interface Props {
  containers: ContainerInfo[];
}

const StackStatsToggle = ({ containers }: Props) => {
  const [active, setActive] = useState(false);
  const { t } = useTranslation("containers");

  const containerIDs = useMemo(
    () => containers.filter((c) => c.State === "running").map((c) => c.Id),
    [containers],
  );

  const snapshot = useStackStats(containerIDs, active);

  if (containerIDs.length === 0) return null;

  if (!active) {
    return (
      <Button
        size="small"
        icon={<BarChartOutlined />}
        onClick={() => setActive(true)}
      >
        {t("stats.open")}
      </Button>
    );
  }

  return (
    <>
      <Button
        size="small"
        danger
        icon={<CloseOutlined />}
        onClick={() => setActive(false)}
        style={{ marginBottom: 8 }}
      >
        {t("stats.close")}
      </Button>
      <StackStats snapshot={snapshot} />
    </>
  );
};

export default memo(StackStatsToggle);
