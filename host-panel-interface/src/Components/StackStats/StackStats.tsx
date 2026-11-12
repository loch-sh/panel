import { Badge, Flex, Progress, Table } from "antd";
import type { ColumnsType } from "antd/es/table";
import { memo, useMemo } from "react";
import { useTranslation } from "react-i18next";
import type { ContainerStats } from "../../Services/Containers/Models/stats.model";
import type { StatsSnapshot } from "../../Services/Containers/Hooks/useStackStats";
import Sparkline from "./Sparkline";

function formatBytes(bytes: number): string {
  if (bytes === 0) return "0 B";
  const units = ["B", "KB", "MB", "GB", "TB"];
  const i = Math.floor(Math.log(bytes) / Math.log(1024));
  const val = bytes / 1024 ** i;
  return `${val.toFixed(val < 10 ? 1 : 0)} ${units[i]}`;
}

function cpuColor(pct: number): string {
  if (pct > 80) return "#ff4d4f";
  if (pct > 50) return "#faad14";
  return "#52c41a";
}

function memColor(pct: number): string {
  if (pct > 90) return "#ff4d4f";
  if (pct > 70) return "#faad14";
  return "#1677ff";
}

interface StackStatsProps {
  snapshot: StatsSnapshot;
}

const StackStats = ({ snapshot }: StackStatsProps) => {
  const { current, history } = snapshot;
  const { t } = useTranslation("containers");

  const totals = useMemo(() => {
    let cpu = 0;
    let mem = 0;
    let memLimit = 0;
    let netRx = 0;
    let netTx = 0;
    for (const s of current) {
      cpu += s.cpuPct;
      mem += s.memUsage;
      memLimit += s.memLimit;
      netRx += s.netRx;
      netTx += s.netTx;
    }
    return { cpu, mem, memLimit, netRx, netTx };
  }, [current]);

  const columns = useMemo<ColumnsType<ContainerStats>>(
    () => [
      {
        title: t("stats.columns.container"),
        dataIndex: "name",
        width: 180,
        render: (name: string) => (
          <span style={{ fontWeight: 500 }}>{name}</span>
        ),
      },
      {
        title: t("stats.columns.cpu"),
        dataIndex: "cpuPct",
        width: 220,
        render: (pct: number, record) => {
          const cpuHistory =
            history.get(record.id)?.map((s) => s.cpuPct) ?? [];
          return (
            <Flex align="center" gap={8}>
              <Progress
                percent={Math.min(pct, 100)}
                size="small"
                strokeColor={cpuColor(pct)}
                format={() => `${pct.toFixed(1)}%`}
                style={{ width: 90, margin: 0 }}
              />
              <Sparkline
                data={cpuHistory}
                color={cpuColor(pct)}
                max={100}
                width={100}
                height={24}
              />
            </Flex>
          );
        },
      },
      {
        title: t("stats.columns.ram"),
        dataIndex: "memPct",
        width: 200,
        responsive: ["md"],
        render: (pct: number, record) => (
          <Flex align="center" gap={8}>
            <Progress
              percent={Math.min(pct, 100)}
              size="small"
              strokeColor={memColor(pct)}
              format={() => `${pct.toFixed(1)}%`}
              style={{ width: 90, margin: 0 }}
            />
            <span
              style={{ fontSize: 12, color: "#888", whiteSpace: "nowrap" }}
            >
              {formatBytes(record.memUsage)} / {formatBytes(record.memLimit)}
            </span>
          </Flex>
        ),
      },
      {
        title: t("stats.columns.network"),
        width: 160,
        responsive: ["md"],
        render: (_, record) => (
          <span style={{ fontSize: 12, whiteSpace: "nowrap" }}>
            <span style={{ color: "#52c41a" }}>
              {formatBytes(record.netRx)}
            </span>
            {" / "}
            <span style={{ color: "#1677ff" }}>
              {formatBytes(record.netTx)}
            </span>
          </span>
        ),
      },
      {
        title: t("stats.columns.pids"),
        dataIndex: "pids",
        width: 60,
        responsive: ["md"],
      },
    ],
    [history, t],
  );

  return (
    <div style={{ padding: "8px 0" }}>
      <Flex
        align="center"
        gap={16}
        className="stack-stats-summary"
        style={{ marginBottom: 8, fontSize: 13, color: "#888" }}
      >
        <Badge status="processing" text={t("stats.live")} />
        <span>
          {t("stats.cpuTotal")} <b>{totals.cpu.toFixed(1)}%</b>
        </span>
        <span>
          {t("stats.ramLabel")} <b>{formatBytes(totals.mem)}</b> / {formatBytes(totals.memLimit)}
        </span>
        <span>
          {t("stats.netLabel")} <span style={{ color: "#52c41a" }}>{formatBytes(totals.netRx)}</span>
          {" / "}
          <span style={{ color: "#1677ff" }}>{formatBytes(totals.netTx)}</span>
        </span>
      </Flex>

      <Table<ContainerStats>
        columns={columns}
        dataSource={current}
        rowKey="id"
        pagination={current.length > 20 ? { pageSize: 20 } : false}
        size="small"
        scroll={{ x: "max-content" }}
      />
    </div>
  );
};

export default memo(StackStats);
