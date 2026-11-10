import { DisconnectOutlined, WarningOutlined } from "@ant-design/icons";
import { Button, Space, Table, Tag, Tooltip, Typography } from "antd";
import { ColumnsType } from "antd/es/table";
import type { TFunction } from "i18next";
import { useMemo } from "react";
import { useTranslation } from "react-i18next";
import { Link } from "react-router";
import { useContainerLabels } from "../../i18n/useContainerLabels";
import { ServiceInstanceModel } from "../../Services/Databases/Models/databases.model";
import { FunctionSummaryModel } from "../../Services/Functions/Models/functions.model";
import {
  NetworkMemberKind,
  NetworkMemberModel,
} from "../../Services/Networks/Models/networks.model";

const { Text } = Typography;

const KIND_COLORS: Record<NetworkMemberKind, string> = {
  site: "blue",
  instance: "cyan",
  function: "magenta",
  panel: "gold",
  unmanaged: "default",
};

/** Page of a member: the site, or the list of its engine. */
function memberLink(
  m: NetworkMemberModel,
  instances: ReadonlyMap<string, ServiceInstanceModel>,
): string | null {
  if (m.kind === "site") return `/hosting/${encodeURIComponent(m.name)}`;
  if (m.kind === "instance") {
    const inst = instances.get(m.name);
    return inst ? `/services/${encodeURIComponent(inst.engine)}` : null;
  }
  if (m.kind === "function") return `/functions/${encodeURIComponent(m.name)}`;
  return null;
}

/** Members the panel attaches and detaches. */
const isManagedKind = (kind: NetworkMemberKind) =>
  kind === "site" || kind === "instance" || kind === "function";

interface Props {
  members: NetworkMemberModel[];
  instances: ReadonlyMap<string, ServiceInstanceModel>;
  /** functions by name (their networks: a function keeps at least one) */
  functions?: ReadonlyMap<string, FunctionSummaryModel>;
  busy?: boolean;
  /**
   * Detaches a site or an instance; without it the table is read-only (a
   * Docker network the panel did not create: no declaration either).
   */
  onDetach?: (m: NetworkMemberModel) => void;
  /** text shown when there is no member */
  emptyText?: string;
}

function getColumns(
  t: TFunction<"networks">,
  stateLabel: (s: string) => string,
  instances: Props["instances"],
  functions: Props["functions"],
  busy: boolean,
  onDetach: Props["onDetach"],
): ColumnsType<NetworkMemberModel> {
  const columns: ColumnsType<NetworkMemberModel> = [
    {
      title: t("members.columns.kind"),
      dataIndex: "kind",
      render: (kind: NetworkMemberKind) =>
        kind === "panel" ? (
          <Tooltip title={t("members.panelHint")}>
            <Tag color={KIND_COLORS[kind]}>{t(`members.kinds.${kind}`)}</Tag>
          </Tooltip>
        ) : (
          <Tag color={KIND_COLORS[kind]}>{t(`members.kinds.${kind}`)}</Tag>
        ),
    },
    {
      title: t("members.columns.name"),
      dataIndex: "name",
      render: (name: string, m) => {
        const to = memberLink(m, instances);
        return (
          <Space size={4} wrap>
            {to ? (
              <Link to={to} style={{ fontWeight: 600 }}>
                {name}
              </Link>
            ) : (
              <Text strong>{name}</Text>
            )}
            {onDetach && m.expected && !m.present && (
              <Tooltip title={t("members.notConnectedHint")}>
                <Tag color="orange" icon={<WarningOutlined />}>
                  {t("members.notConnected")}
                </Tag>
              </Tooltip>
            )}
            {onDetach && m.present && !m.expected && isManagedKind(m.kind) && (
              <Tooltip title={t("members.notDeclaredHint")}>
                <Tag color="orange" icon={<WarningOutlined />}>
                  {t("members.notDeclared")}
                </Tag>
              </Tooltip>
            )}
          </Space>
        );
      },
    },
    {
      title: t("members.columns.container"),
      dataIndex: "container",
      render: (container: string | undefined, m) =>
        container ? (
          <Tooltip title={m.containerId}>
            <Text code>{container}</Text>
          </Tooltip>
        ) : (
          <Text type="secondary">{t("members.noContainer")}</Text>
        ),
    },
    {
      title: t("members.columns.ipv4"),
      dataIndex: "ipv4",
      render: (ip?: string) =>
        ip ? (
          <Text code copyable={{ text: ip }}>
            {ip}
          </Text>
        ) : (
          <Text type="secondary">—</Text>
        ),
    },
    {
      title: t("members.columns.mac"),
      dataIndex: "mac",
      responsive: ["lg"],
      render: (mac?: string) =>
        mac ? <Text code>{mac}</Text> : <Text type="secondary">—</Text>,
    },
    {
      title: t("members.columns.state"),
      dataIndex: "state",
      render: (state?: string) =>
        state ? stateLabel(state) : <Text type="secondary">—</Text>,
    },
  ];
  if (!onDetach) return columns;
  return [
    ...columns,
    {
      title: t("members.columns.actions"),
      width: 110,
      render: (_: unknown, m) => {
        if (!isManagedKind(m.kind)) return null;
        // An instance or a function keeps at least one network.
        const inst = m.kind === "instance" ? instances.get(m.name) : undefined;
        const fn = m.kind === "function" ? functions?.get(m.name) : undefined;
        const last =
          (!!inst && inst.networks.length <= 1) ||
          (!!fn && fn.networks.length <= 1);
        const button = (
          <Button
            size="small"
            danger
            icon={<DisconnectOutlined />}
            disabled={busy || last}
            onClick={() => onDetach(m)}
          >
            {t("members.detach")}
          </Button>
        );
        return last ? (
          <Tooltip
            title={
              fn
                ? t("members.lastNetworkHintFunction")
                : t("members.lastNetworkHint")
            }
          >
            {button}
          </Tooltip>
        ) : (
          button
        );
      },
    },
  ];
}

/** Managed members of a network (unmanaged containers are listed apart). */
export default function MembersTable({
  members,
  instances,
  functions,
  busy = false,
  onDetach,
  emptyText,
}: Readonly<Props>) {
  const { t } = useTranslation("networks");
  const { stateLabel } = useContainerLabels();
  const columns = useMemo(
    () => getColumns(t, stateLabel, instances, functions, busy, onDetach),
    [t, stateLabel, instances, functions, busy, onDetach],
  );
  const locale = useMemo(
    () => ({ emptyText: emptyText ?? t("detail.membersEmpty") }),
    [t, emptyText],
  );
  return (
    <Table<NetworkMemberModel>
      columns={columns}
      dataSource={members}
      rowKey={(m) => `${m.kind}/${m.name}/${m.containerId ?? ""}`}
      pagination={false}
      size="middle"
      scroll={{ x: "max-content" }}
      locale={locale}
    />
  );
}
