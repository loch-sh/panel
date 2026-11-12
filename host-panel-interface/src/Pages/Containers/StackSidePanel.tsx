import {
  ApartmentOutlined,
  ApiOutlined,
  AppstoreOutlined,
  ArrowsAltOutlined,
  BarChartOutlined,
  CheckCircleOutlined,
  ClusterOutlined,
  ExportOutlined,
  FileTextOutlined,
  FolderOutlined,
  InfoCircleOutlined,
  ShrinkOutlined,
} from "@ant-design/icons";
import {
  Avatar,
  Badge,
  Button,
  Card,
  Col,
  Descriptions,
  Drawer,
  Empty,
  Flex,
  Row,
  Space,
  Statistic,
  Table,
  Tabs,
  Tag,
  Tooltip,
  Typography,
  theme,
} from "antd";
import type { BadgeProps } from "antd";
import type { ColumnsType } from "antd/es/table";
import type { ContainerInfo } from "dockerode";
import { type ReactNode, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { Link } from "react-router";
import StackStatusTag from "../../Components/Base/StackStatusTag";
import StackStats from "../../Components/StackStats/StackStats";
import { useContainerLabels } from "../../i18n/useContainerLabels";
import useStackStats from "../../Services/Containers/Hooks/useStackStats";
import { containerStateBadge } from "../../Utils/containerState";
import { displayContainerName } from "../../Utils/hostedStack";
import type { StackGroup } from "./stackGroup";

const { Text } = Typography;

const LABEL = {
  service: "com.docker.compose.service",
  workingDir: "com.docker.compose.project.working_dir",
  configFiles: "com.docker.compose.project.config_files",
  version: "com.docker.compose.version",
  dependsOn: "com.docker.compose.depends_on",
} as const;

type TabKey = "services" | "compose" | "networks" | "mounts" | "monitoring";

interface Props {
  /** The stack shown (undefined: closed). Its containers stay live. */
  stack?: StackGroup;
  onClose: () => void;
  /** Opens the detail of a container. */
  onOpenContainer: (id: string) => void;
  /** Start / stop / restart buttons of the stack. */
  actions?: ReactNode;
}

interface ServiceRow {
  id: string;
  service: string;
  name: string;
  state: string;
  status: string;
  image: string;
  ports: PortLabel[];
  dependsOn: string[];
}

/** A port of a container: published on the host, or only exposed. */
interface PortLabel {
  label: string;
  published: boolean;
}

interface NetworkRow {
  network: string;
  members: { id: string; name: string; ip: string }[];
}

interface MountRow {
  key: string;
  container: string;
  type: string;
  source: string;
  destination: string;
  readOnly: boolean;
}

const containerName = (c: ContainerInfo) =>
  displayContainerName((c.Names[0] ?? c.Id).replace(/^\//, ""));

/** "8080→80/tcp" (published), or "80/tcp" (only exposed); the IPv4 and
 * IPv6 bindings of one port count once. */
function portsOf(c: ContainerInfo): PortLabel[] {
  const ports = new Map<string, PortLabel>();
  for (const p of c.Ports ?? []) {
    const published = !!p.PublicPort;
    const label = published
      ? `${p.PublicPort}→${p.PrivatePort}/${p.Type}`
      : `${p.PrivatePort}/${p.Type}`;
    ports.set(label, { label, published });
  }
  return [...ports.values()].sort((a, b) => a.label.localeCompare(b.label));
}

/** Services of depends_on ("db:service_started:false,cache:..."). */
function dependsOnOf(c: ContainerInfo): string[] {
  const raw = c.Labels?.[LABEL.dependsOn] ?? "";
  return raw
    .split(",")
    .map((d) => d.split(":")[0].trim())
    .filter(Boolean);
}

/** Badge status of a stack: an error exit, all running, or some stopped. */
function stackBadge(stack: StackGroup): BadgeProps["status"] {
  if (stack.hasErrorExits) return "error";
  return stack.runningCount === stack.totalCount ? "success" : "warning";
}

function distinct(values: (string | undefined)[]): string[] {
  return [...new Set(values.filter((v): v is string => !!v))].sort();
}

export default function StackSidePanel({
  stack,
  onClose,
  onOpenContainer,
  actions,
}: Readonly<Props>) {
  const { t } = useTranslation("containers");
  const { stateLabel, statusLabel } = useContainerLabels();
  const { token } = theme.useToken();
  const [tab, setTab] = useState<TabKey>("services");
  const [fullSize, setFullSize] = useState(false);
  const containers = useMemo(() => stack?.containers ?? [], [stack]);

  const services: ServiceRow[] = useMemo(
    () =>
      containers
        .map((c) => ({
          id: c.Id,
          service: c.Labels?.[LABEL.service] ?? containerName(c),
          name: containerName(c),
          state: c.State,
          status: c.Status,
          image: c.Image,
          ports: portsOf(c),
          dependsOn: dependsOnOf(c),
        }))
        .sort((a, b) => a.service.localeCompare(b.service)),
    [containers],
  );

  const networks: NetworkRow[] = useMemo(() => {
    const byNetwork = new Map<string, NetworkRow["members"]>();
    for (const c of containers) {
      for (const [net, settings] of Object.entries(
        c.NetworkSettings?.Networks ?? {},
      )) {
        if (!byNetwork.has(net)) byNetwork.set(net, []);
        byNetwork
          .get(net)!
          .push({ id: c.Id, name: containerName(c), ip: settings.IPAddress ?? "" });
      }
    }
    return [...byNetwork.entries()]
      .map(([network, members]) => ({ network, members }))
      .sort((a, b) => a.network.localeCompare(b.network));
  }, [containers]);

  const mounts: MountRow[] = useMemo(
    () =>
      containers.flatMap((c) =>
        (c.Mounts ?? []).map((m) => ({
          key: `${c.Id}:${m.Destination}`,
          container: containerName(c),
          type: m.Type,
          source: m.Name || m.Source,
          destination: m.Destination,
          readOnly: !m.RW,
        })),
      ),
    [containers],
  );

  const compose = useMemo(() => {
    const label = (c: ContainerInfo, name: string) => c.Labels?.[name];
    return {
      workingDirs: distinct(containers.map((c) => label(c, LABEL.workingDir))),
      configFiles: distinct(
        containers.flatMap((c) => (label(c, LABEL.configFiles) ?? "").split(",")),
      ),
      versions: distinct(containers.map((c) => label(c, LABEL.version))),
    };
  }, [containers]);

  const runningIds = useMemo(
    () => containers.filter((c) => c.State === "running").map((c) => c.Id),
    [containers],
  );
  const snapshot = useStackStats(
    runningIds,
    !!stack && tab === "monitoring" && runningIds.length > 0,
  );

  const publishedPorts = useMemo(
    () =>
      services.reduce((n, s) => n + s.ports.filter((p) => p.published).length, 0),
    [services],
  );

  const serviceColumns: ColumnsType<ServiceRow> = useMemo(() => [
    {
      title: t("stackPanel.columns.service"),
      dataIndex: "service",
      render: (service: string, row) => (
        <Flex vertical>
          <Text strong>{service}</Text>
          {row.name !== service && (
            <Text type="secondary" style={{ fontSize: token.fontSizeSM }}>
              {row.name}
            </Text>
          )}
        </Flex>
      ),
    },
    {
      title: t("stackPanel.columns.state"),
      dataIndex: "state",
      render: (state: string, row) => (
        <Tooltip title={statusLabel(row.status)}>
          <Badge status={containerStateBadge(state)} text={stateLabel(state)} />
        </Tooltip>
      ),
    },
    {
      title: t("stackPanel.columns.image"),
      dataIndex: "image",
      ellipsis: { showTitle: true },
      render: (image: string) => <Text type="secondary">{image}</Text>,
    },
    {
      title: t("stackPanel.columns.ports"),
      dataIndex: "ports",
      render: (ports: PortLabel[]) =>
        ports.length === 0 ? (
          "—"
        ) : (
          <Flex gap={4} wrap>
            {ports.map((p) => (
              // Published on the host: green; only exposed: neutral.
              <Tag
                key={p.label}
                color={p.published ? "green" : undefined}
                style={{ marginInlineEnd: 0 }}
              >
                {p.label}
              </Tag>
            ))}
          </Flex>
        ),
    },
    {
      title: t("stackPanel.columns.dependsOn"),
      dataIndex: "dependsOn",
      render: (deps: string[]) =>
        deps.length === 0 ? (
          "—"
        ) : (
          <Flex gap={4} wrap>
            {deps.map((d) => (
              <Tag key={d} color="geekblue" style={{ marginInlineEnd: 0 }}>
                {d}
              </Tag>
            ))}
          </Flex>
        ),
    },
    {
      key: "detail",
      align: "right",
      render: (_, row) => (
        <Tooltip title={t("stackPanel.containerDetail")}>
          <Button
            aria-label={t("stackPanel.containerDetail")}
            size="small"
            icon={<InfoCircleOutlined />}
            onClick={() => onOpenContainer(row.id)}
          />
        </Tooltip>
      ),
    },
  ], [t, stateLabel, statusLabel, token.fontSizeSM, onOpenContainer]);

  const mountColumns: ColumnsType<MountRow> = useMemo(
    () => [
                        { title: t("stackPanel.columns.container"), dataIndex: "container" },
                        { title: t("mountTable.type"), dataIndex: "type" },
                        {
                          title: t("mountTable.source"),
                          dataIndex: "source",
                          render: (v: string) => (
                            <Text style={{ wordBreak: "break-all" }}>{v}</Text>
                          ),
                        },
                        {
                          title: t("mountTable.destination"),
                          dataIndex: "destination",
                          render: (v: string, row: MountRow) => (
                            <Space size={4}>
                              <Text code>{v}</Text>
                              {row.readOnly && <Tag>{t("stackPanel.readOnly")}</Tag>}
                            </Space>
                          ),
                        },
                      ],
    [t],
  );

  const title = stack && (
    <Flex align="center" gap={12} style={{ minWidth: 0 }}>
      <Badge
        dot
        status={stackBadge(stack)}
        offset={[-4, 32]}
      >
        <Avatar
          shape="square"
          size={40}
          icon={<AppstoreOutlined />}
          style={{ background: token.colorPrimaryBg, color: token.colorPrimary }}
        />
      </Badge>
      <Flex vertical style={{ minWidth: 0 }}>
        <Text strong ellipsis={{ tooltip: stack.stackName }} style={{ fontSize: token.fontSizeLG }}>
          {stack.stackName}
        </Text>
        <Text type="secondary" style={{ fontSize: token.fontSizeSM, fontWeight: "normal" }}>
          {t("stackPanel.subtitle")}
        </Text>
      </Flex>
    </Flex>
  );

  const figures = stack
    ? [
        {
          key: "running",
          title: t("stackPanel.summary.running"),
          value: `${stack.runningCount}/${stack.totalCount}`,
          icon: <CheckCircleOutlined />,
        },
        {
          key: "services",
          title: t("stackPanel.summary.services"),
          value: distinct(services.map((s) => s.service)).length,
          icon: <AppstoreOutlined />,
        },
        {
          key: "ports",
          title: t("sidePanel.summary.ports"),
          value: publishedPorts,
          icon: <ApiOutlined />,
        },
        {
          key: "networks",
          title: t("stackPanel.summary.networks"),
          value: networks.length,
          icon: <ClusterOutlined />,
        },
      ]
    : [];

  return (
    <Drawer
      closable
      destroyOnHidden
      open={!!stack}
      onClose={() => {
        setTab("services");
        setFullSize(false);
        onClose();
      }}
      placement="right"
      title={title}
      size={fullSize ? "100vw" : "large"}
      extra={
        <Space>
          {actions}
          <Tooltip title={t("sidePanel.fullScreen")}>
            <Button
              aria-label={t("sidePanel.fullScreen")}
              type={fullSize ? "primary" : "default"}
              icon={fullSize ? <ShrinkOutlined /> : <ArrowsAltOutlined />}
              onClick={() => setFullSize((v) => !v)}
            />
          </Tooltip>
        </Space>
      }
      styles={{ body: { paddingTop: 16 } }}
    >
      {stack && (
        <Flex vertical gap={16}>
          <Flex gap={8} wrap align="center">
            <StackStatusTag
              runningCount={stack.runningCount}
              healthyCount={stack.healthyCount}
              totalCount={stack.totalCount}
              style={{ marginInlineEnd: 0 }}
            />
            {stack.hasErrorExits && (
              <Tag color="error">{t("stackPanel.errorExits")}</Tag>
            )}
            {stack.hosted && (
              <Link to={`/hosting/${encodeURIComponent(stack.stackName)}`}>
                <Tag color="blue" icon={<ExportOutlined />} style={{ cursor: "pointer" }}>
                  {t("stackPanel.openSite")}
                </Tag>
              </Link>
            )}
            {stack.serviceEngine && (
              <Link to={`/services/${stack.serviceEngine}`}>
                <Tag color="purple" icon={<ExportOutlined />} style={{ cursor: "pointer" }}>
                  {t("stackPanel.openService")}
                </Tag>
              </Link>
            )}
          </Flex>

          <Row gutter={[12, 12]}>
            {figures.map((s) => (
              <Col key={s.key} xs={12} md={6}>
                <Card size="small" style={{ height: "100%" }}>
                  <Statistic
                    title={
                      <Space size={6}>
                        {s.icon}
                        {s.title}
                      </Space>
                    }
                    value={s.value}
                    valueStyle={{ fontSize: token.fontSizeHeading4 }}
                  />
                </Card>
              </Col>
            ))}
          </Row>

          <Tabs
            activeKey={tab}
            onChange={(key) => setTab(key as TabKey)}
            destroyOnHidden
            items={[
              {
                key: "services",
                label: t("stackPanel.tabs.services"),
                icon: <AppstoreOutlined />,
                children: (
                  <Table
                    size="small"
                    rowKey="id"
                    columns={serviceColumns}
                    dataSource={services}
                    pagination={services.length > 20 ? undefined : false}
                    scroll={{ x: "max-content" }}
                    onRow={(row) => ({
                      onDoubleClick: () => onOpenContainer(row.id),
                    })}
                  />
                ),
              },
              {
                key: "compose",
                label: t("stackPanel.tabs.compose"),
                icon: <FileTextOutlined />,
                children: (
                  <Descriptions
                    bordered
                    size="small"
                    column={1}
                    styles={{ label: { width: 190 } }}
                    items={[
                      {
                        key: "project",
                        label: t("stackPanel.compose.project"),
                        children: (
                          <Text code copyable>
                            {stack.key}
                          </Text>
                        ),
                      },
                      {
                        key: "workingDir",
                        label: t("stackPanel.compose.workingDir"),
                        children:
                          compose.workingDirs.length === 0 ? (
                            "—"
                          ) : (
                            <Flex vertical gap={4}>
                              {compose.workingDirs.map((d) => (
                                <Text key={d} copyable style={{ wordBreak: "break-all" }}>
                                  {d}
                                </Text>
                              ))}
                            </Flex>
                          ),
                      },
                      {
                        key: "configFiles",
                        label: t("stackPanel.compose.configFiles"),
                        children:
                          compose.configFiles.length === 0 ? (
                            "—"
                          ) : (
                            <Flex vertical gap={4}>
                              {compose.configFiles.map((f) => (
                                <Text key={f} copyable style={{ wordBreak: "break-all" }}>
                                  {f}
                                </Text>
                              ))}
                            </Flex>
                          ),
                      },
                      {
                        key: "version",
                        label: t("stackPanel.compose.version"),
                        children: compose.versions.join(", ") || "—",
                      },
                    ]}
                  />
                ),
              },
              {
                key: "networks",
                label: t("stackPanel.tabs.networks"),
                icon: <ApartmentOutlined />,
                children:
                  networks.length === 0 ? (
                    <Empty description={t("sidePanel.none")} />
                  ) : (
                    <Descriptions
                      bordered
                      size="small"
                      column={1}
                      styles={{ label: { width: 190 } }}
                      items={networks.map((n) => ({
                        key: n.network,
                        label: <Text strong>{n.network}</Text>,
                        children: (
                          <Flex vertical gap={4}>
                            {n.members.map((m) => (
                              <Flex key={m.id} gap={8} align="center" wrap>
                                <Text>{m.name}</Text>
                                {m.ip && <Text code>{m.ip}</Text>}
                              </Flex>
                            ))}
                          </Flex>
                        ),
                      }))}
                    />
                  ),
              },
              {
                key: "mounts",
                label: t("sidePanel.summary.mounts"),
                icon: <FolderOutlined />,
                children:
                  mounts.length === 0 ? (
                    <Empty description={t("sidePanel.none")} />
                  ) : (
                    <Table
                      size="small"
                      rowKey="key"
                      dataSource={mounts}
                      pagination={mounts.length > 20 ? undefined : false}
                      scroll={{ x: "max-content" }}
                      columns={mountColumns}
                    />
                  ),
              },
              {
                key: "monitoring",
                label: t("stackPanel.tabs.monitoring"),
                icon: <BarChartOutlined />,
                children:
                  runningIds.length === 0 ? (
                    <Empty description={t("stackPanel.noRunning")} />
                  ) : (
                    <StackStats snapshot={snapshot} />
                  ),
              },
            ]}
          />
        </Flex>
      )}
    </Drawer>
  );
}
