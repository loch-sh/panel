import { Table, Tag, Tooltip, Typography } from "antd";
import { ColumnsType } from "antd/es/table";
import type { TFunction } from "i18next";
import { useMemo } from "react";
import { useTranslation } from "react-i18next";
import { Link as RouterLink } from "react-router";
import { DomainEntry } from "../../Services/Domains/Models/domains.model";
import { useContainerLabels } from "../../i18n/useContainerLabels";

const { Text, Link } = Typography;

interface FlatRow {
  key: string;
  domain: string;
  routerName: string;
  rule: string;
  entrypoints: string[];
  tls: boolean;
  certResolver?: string;
  middlewares: string[];
  serviceName: string;
  servicePort?: number;
  containerName: string;
  containerState: string;
  containerImage: string;
}

function flattenEntries(entries: DomainEntry[]): FlatRow[] {
  const rows: FlatRow[] = [];

  for (const entry of entries) {
    const servicePortMap = new Map<string, number>();
    for (const svc of entry.services) {
      if (svc.port) servicePortMap.set(svc.name, svc.port);
    }

    for (const router of entry.routers) {
      const domains = router.domains.length > 0 ? router.domains : ["—"];

      for (const domain of domains) {
        rows.push({
          key: `${entry.containerId}-${router.name}-${domain}`,
          domain,
          routerName: router.name,
          rule: router.rule,
          entrypoints: router.entrypoints,
          tls: router.tls,
          certResolver: router.certResolver,
          middlewares: router.middlewares ?? [],
          serviceName: router.serviceName,
          servicePort: servicePortMap.get(router.serviceName),
          containerName: entry.containerName,
          containerState: entry.containerState,
          containerImage: entry.containerImage,
        });
      }
    }
  }

  return rows;
}

const stateColorMap: Record<string, string> = {
  running: "green",
  exited: "red",
  created: "blue",
  paused: "orange",
  restarting: "orange",
  dead: "red",
};

function getColumns(
  t: TFunction<"domains">,
  stateLabel: (state: string) => string,
): ColumnsType<FlatRow> {
  return [
    {
      title: t("table.columns.domain"),
      dataIndex: "domain",
      sorter: (a, b) => a.domain.localeCompare(b.domain),
      render: (domain: string) =>
        domain === "—" ? (
          <Text type="secondary">—</Text>
        ) : (
          <Link href={`https://${domain}`} target="_blank">
            {domain}
          </Link>
        ),
    },
    {
      title: t("table.columns.router"),
      dataIndex: "routerName",
      responsive: ["md"],
      render: (name: string) => <Text code>{name}</Text>,
    },
    {
      title: t("table.columns.entrypoints"),
      dataIndex: "entrypoints",
      responsive: ["md"],
      render: (eps: string[]) =>
        eps.map((ep) => (
          <Tag key={ep} color={ep === "websecure" ? "blue" : "default"}>
            {ep}
          </Tag>
        )),
    },
    {
      title: t("table.columns.tls"),
      dataIndex: "tls",
      width: 80,
      responsive: ["md"],
      render: (tls: boolean, record: FlatRow) => (
        <Tooltip title={record.certResolver ?? undefined}>
          <Tag color={tls ? "green" : "red"}>{tls
              ? t("actions.yes", { ns: "common" })
              : t("actions.no", { ns: "common" })}</Tag>
        </Tooltip>
      ),
    },
    {
      title: t("table.columns.middlewares"),
      dataIndex: "middlewares",
      responsive: ["md"],
      render: (mws: string[]) =>
        mws.length > 0 ? (
          mws.map((mw) => (
            <Tag key={mw} color="purple">
              {mw}
            </Tag>
          ))
        ) : (
          <Text type="secondary">—</Text>
        ),
    },
    {
      title: t("table.columns.service"),
      responsive: ["md"],
      render: (_: unknown, record: FlatRow) => (
        <Text code>
          {record.serviceName}
          {record.servicePort ? `:${record.servicePort}` : ""}
        </Text>
      ),
    },
    {
      title: t("table.columns.container"),
      dataIndex: "containerName",
      sorter: (a, b) => a.containerName.localeCompare(b.containerName),
      render: (name: string) => (
        <RouterLink
          to={`/containers?search=${encodeURIComponent(name)}`}
          style={{ fontWeight: 600 }}
        >
          {name}
        </RouterLink>
      ),
    },
    {
      title: t("table.columns.state"),
      dataIndex: "containerState",
      width: 100,
      responsive: ["md"],
      render: (state: string) => (
        <Tag color={stateColorMap[state] ?? "default"}>
          {stateLabel(state)}
        </Tag>
      ),
    },
    {
      title: t("table.columns.image"),
      dataIndex: "containerImage",
      ellipsis: true,
      responsive: ["md"],
      render: (image: string) => (
        <Tooltip title={image}>
          <Text type="secondary" style={{ fontSize: 12 }}>
            {image.split("@")[0]}
          </Text>
        </Tooltip>
      ),
    },
  ];
}

interface Props {
  data: DomainEntry[];
}

export default function DomainsTable({ data }: Props) {
  const { t } = useTranslation("domains");
  const rows = useMemo(() => flattenEntries(data), [data]);
  const { stateLabel } = useContainerLabels();
  const columns = useMemo(() => getColumns(t, stateLabel), [t, stateLabel]);

  return (
    <Table<FlatRow>
      columns={columns}
      dataSource={rows}
      rowKey="key"
      size="small"
      pagination={rows.length > 50 ? { pageSize: 50 } : false}
      scroll={{ x: "max-content" }}
    />
  );
}
