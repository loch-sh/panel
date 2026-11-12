import {
  ApiOutlined,
  ArrowsAltOutlined,
  CalculatorOutlined,
  ClockCircleOutlined,
  CodeOutlined,
  DisconnectOutlined,
  DockerOutlined,
  DownloadOutlined,
  FolderOutlined,
  HeartOutlined,
  InfoCircleOutlined,
  ProfileOutlined,
  RedoOutlined,
  ShrinkOutlined,
} from "@ant-design/icons";
import {
  Alert,
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
  Tabs,
  Tag,
  Tooltip,
  Typography,
  theme,
} from "antd";
import type { ContainerInspectInfo } from "dockerode";
import { format, formatDistanceToNowStrict, parseISO } from "date-fns";
import {
  type ReactNode,
  useCallback,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
} from "react";
import { Trans, useTranslation } from "react-i18next";
import { Link } from "react-router";
import { appConfig } from "../../config";
import { useContainerLabels } from "../../i18n/useContainerLabels";
import { containerStateBadge } from "../../Utils/containerState";
import { useDateFnsLocale } from "../../i18n/useLocales";
import useContainer from "../../Services/Containers/Hooks/useContainer";
import {
  displayContainerName,
  displayStackName,
} from "../../Utils/hostedStack";
import LogViewer from "../LogViewer/LogViewer";
import MountTable from "../MountTable/MountTable";
import NetworkTable from "../NetworkTable/NetworkTable";
import TerminalInterface, {
  type TerminalHandle,
} from "../Terminal/TerminalInterface";

const { Text, Title } = Typography;

interface Props {
  open: boolean;
  onClose: () => void;
  id?: string;
}

type TabKey = "overview" | "network" | "mounts" | "logs" | "terminal";


type HealthStatus = "healthy" | "unhealthy" | "starting";

const HEALTH_COLOR: Record<HealthStatus, string> = {
  healthy: "success",
  unhealthy: "error",
  starting: "processing",
};

/** A Docker health status the panel can label ("none" and unknown: no). */
function healthOf(status: string | undefined): HealthStatus | undefined {
  return status && status in HEALTH_COLOR ? (status as HealthStatus) : undefined;
}

/** Docker reports "0001-01-01T00:00:00Z" for a date that never happened. */
function dockerDate(value?: string): Date | undefined {
  if (!value) return undefined;
  const date = parseISO(value);
  return Number.isNaN(date.getTime()) || date.getFullYear() < 2000
    ? undefined
    : date;
}

/** Published host ports (an exposed port without binding is not counted). */
function publishedPorts(info: ContainerInspectInfo): number {
  return Object.values(info.NetworkSettings.Ports ?? {}).reduce(
    (n, bindings) => n + (bindings?.length ?? 0),
    0,
  );
}

/** Minimum height of the logs and the terminal in their tab. */
const MIN_PANE_HEIGHT = 400;
/** The drawer body's bottom padding. */
const PANE_BOTTOM_GAP = 24;

/**
 * FillHeight gives its child the height left down to the bottom of the
 * window (at least MIN_PANE_HEIGHT): the logs and the terminal use the
 * whole tab. Measured again on a window resize and when `layoutKey`
 * changes (full screen, console only).
 */
function FillHeight({
  layoutKey,
  children,
}: Readonly<{
  layoutKey: string;
  children: (height: number) => ReactNode;
}>) {
  const ref = useRef<HTMLDivElement>(null);
  const [height, setHeight] = useState(MIN_PANE_HEIGHT);
  useLayoutEffect(() => {
    const update = () => {
      const top = ref.current?.getBoundingClientRect().top ?? 0;
      setHeight(
        Math.max(
          MIN_PANE_HEIGHT,
          Math.floor(window.innerHeight - top - PANE_BOTTOM_GAP),
        ),
      );
    };
    update();
    let frame = 0;
    const onResize = () => {
      cancelAnimationFrame(frame);
      frame = requestAnimationFrame(update); // at most once a frame
    };
    window.addEventListener("resize", onResize);
    return () => {
      cancelAnimationFrame(frame);
      window.removeEventListener("resize", onResize);
    };
  }, [layoutKey]);
  return <div ref={ref}>{children(height)}</div>;
}

function openDetachedTerminal(id: string, name: string) {
  window.open(
    `/terminal/${id}?name=${encodeURIComponent(name)}`,
    `terminal-${id}`,
    "width=900,height=600,menubar=no,toolbar=no,location=no,status=no",
  );
}

export default function ContainerSidePanel({
  open,
  onClose,
  id,
}: Readonly<Props>) {
  const [fullSize, setFullSize] = useState(false);
  const [consoleOnly, setConsoleOnly] = useState(false);
  const [terminalNeeded, setTerminalNeeded] = useState(false);
  const [tab, setTab] = useState<TabKey>("overview");
  const terminalHandleRef = useRef<TerminalHandle>(null);
  const { t } = useTranslation("containers");
  const { t: tc } = useTranslation("common");
  const { stateLabel } = useContainerLabels();
  const dateLocale = useDateFnsLocale();
  const { token } = theme.useToken();

  const { isLoading, data } = useContainer({ id });

  const running = !!data?.State.Running;
  const terminalAvailable = !!data?.Config.Tty;
  const name = data ? displayContainerName(data.Name.substring(1)) : "";

  const handleClose = useCallback(() => {
    setFullSize(false);
    setConsoleOnly(false);
    setTerminalNeeded(false);
    setTab("overview");
    onClose?.();
  }, [onClose]);

  const formatDate = useCallback(
    (date: Date) =>
      t("sidePanel.startedAt.value", {
        date: format(date, "P", { locale: dateLocale }),
        time: format(date, "pp", { locale: dateLocale }),
      }),
    [t, dateLocale],
  );

  const summary = useMemo(() => {
    if (!data) return undefined;
    const startedAt = dockerDate(data.State.StartedAt);
    const finishedAt = dockerDate(data.State.FinishedAt);
    const since = running ? startedAt : finishedAt;
    return {
      startedAt,
      created: dockerDate(data.Created),
      uptimeTitle: running
        ? t("sidePanel.summary.upFor")
        : t("sidePanel.summary.stoppedFor"),
      uptime: since
        ? formatDistanceToNowStrict(since, { locale: dateLocale })
        : "—",
      ports: publishedPorts(data),
      stateBadge: containerStateBadge(data.State.Status),
      health: healthOf(data.State.Health?.Status),
      project: data.Config.Labels?.["com.docker.compose.project"],
      networks: Object.entries(data.NetworkSettings.Networks ?? {}),
    };
  }, [data, running, t, dateLocale]);

  const terminal = (
    <Flex vertical gap={12}>
      {!terminalAvailable && (
        <Alert
          type="warning"
          showIcon
          message={
            <Trans
              t={t}
              i18nKey="sidePanel.terminal.unavailable"
              components={{ code: <code /> }}
            />
          }
        />
      )}
      {terminalAvailable && !running && (
        <Alert type="info" showIcon message={t("sidePanel.terminal.notRunning")} />
      )}
      {terminalAvailable && running && id && (
        <>
          <Flex gap={8} wrap>
            {terminalNeeded ? (
              <Button danger onClick={() => setTerminalNeeded(false)}>
                {tc("actions.close")}
              </Button>
            ) : (
              <Button
                type="primary"
                icon={<CodeOutlined />}
                onClick={() => setTerminalNeeded(true)}
              >
                {t("sidePanel.terminal.open")}
              </Button>
            )}
            <Button
              icon={<DisconnectOutlined />}
              onClick={() => {
                const content = terminalHandleRef.current?.getBufferContent();
                if (content) {
                  sessionStorage.setItem(`terminal-history-${id}`, content);
                }
                setTerminalNeeded(false);
                openDetachedTerminal(id, name);
              }}
            >
              {t("sidePanel.terminal.detachPopup")}
            </Button>
          </Flex>
          {terminalNeeded && (
            <FillHeight layoutKey={`${fullSize}-${consoleOnly}`}>
              {(height) => (
                <div
                  style={{
                    height,
                    borderRadius: token.borderRadiusLG,
                    overflow: "hidden",
                  }}
                >
                  <TerminalInterface ref={terminalHandleRef} id={id} />
                </div>
              )}
            </FillHeight>
          )}
        </>
      )}
    </Flex>
  );

  const title = data ? (
    <Flex align="center" gap={12} style={{ minWidth: 0 }}>
      <Badge dot status={summary?.stateBadge} offset={[-4, 32]}>
        <Avatar
          shape="square"
          size={40}
          icon={<DockerOutlined />}
          style={{ background: token.colorPrimaryBg, color: token.colorPrimary }}
        />
      </Badge>
      <Flex vertical style={{ minWidth: 0 }}>
        <Text strong ellipsis={{ tooltip: name }} style={{ fontSize: token.fontSizeLG }}>
          {name}
        </Text>
        <Text type="secondary" ellipsis={{ tooltip: data.Config.Image }} style={{ fontSize: token.fontSizeSM, fontWeight: "normal" }}>
          {data.Config.Image}
        </Text>
      </Flex>
    </Flex>
  ) : (
    t("sidePanel.title")
  );

  const extra = (
    <Space>
      {running && terminalAvailable && (
        <Tooltip title={t("sidePanel.consoleOnly")}>
          <Button
            aria-label={t("sidePanel.consoleOnly")}
            type={consoleOnly ? "primary" : "default"}
            icon={<CalculatorOutlined />}
            onClick={() => {
              setConsoleOnly((v) => !v);
              setTab("terminal");
              setTerminalNeeded(true);
            }}
          />
        </Tooltip>
      )}
      <Tooltip title={t("sidePanel.fullScreen")}>
        <Button
          aria-label={t("sidePanel.fullScreen")}
          type={fullSize ? "primary" : "default"}
          icon={fullSize ? <ShrinkOutlined /> : <ArrowsAltOutlined />}
          onClick={() => setFullSize((v) => !v)}
        />
      </Tooltip>
    </Space>
  );

  return (
    <Drawer
      closable
      destroyOnHidden
      open={open}
      onClose={handleClose}
      placement="right"
      title={title}
      extra={data ? extra : undefined}
      loading={isLoading}
      size={fullSize ? "100vw" : "large"}
      styles={{ body: { paddingTop: 16 } }}
    >
      {!data && <Empty description={t("sidePanel.noData")} />}

      {/* One tree position for the terminal: "console only" hides the rest
          instead of moving it (a remount would end the shell session). */}
      {!!data && open && summary && (
        <Flex vertical gap={16}>
          {!consoleOnly && (
          <Flex gap={8} wrap>
            <Tag color={summary.stateBadge === "default" ? undefined : summary.stateBadge}>
              {stateLabel(data.State.Status)}
            </Tag>
            {summary.health && (
              <Tag color={HEALTH_COLOR[summary.health]} icon={<HeartOutlined />}>
                {tc(`container.health.${summary.health}`)}
              </Tag>
            )}
            {!running && data.State.ExitCode !== 0 && (
              <Tag color="error">
                {t("sidePanel.summary.exitCode", { code: data.State.ExitCode })}
              </Tag>
            )}
            {summary.project ? (
              <Tag icon={<ApiOutlined />}>
                {t("sidePanel.stack", {
                  stack: displayStackName(summary.project),
                })}
              </Tag>
            ) : (
              <Tag>{t("sidePanel.standalone")}</Tag>
            )}
          </Flex>
          )}

          {!consoleOnly && (
          <Row gutter={[12, 12]}>
            {[
              {
                key: "uptime",
                title: summary.uptimeTitle,
                value: summary.uptime,
                icon: <ClockCircleOutlined />,
              },
              {
                key: "restarts",
                title: t("sidePanel.summary.restarts"),
                value: data.RestartCount,
                icon: <RedoOutlined />,
              },
              {
                key: "ports",
                title: t("sidePanel.summary.ports"),
                value: summary.ports,
                icon: <ApiOutlined />,
              },
              {
                key: "mounts",
                title: t("sidePanel.summary.mounts"),
                value: data.Mounts?.length ?? 0,
                icon: <FolderOutlined />,
              },
            ].map((s) => (
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
          )}

          <Tabs
            activeKey={tab}
            onChange={(key) => setTab(key as TabKey)}
            renderTabBar={consoleOnly ? () => <></> : undefined}
            destroyOnHidden
            items={[
              {
                key: "overview",
                label: t("sidePanel.tabs.overview"),
                icon: <InfoCircleOutlined />,
                children: (
                  <Descriptions
                    bordered
                    size="small"
                    column={1}
                    styles={{ label: { width: 170 } }}
                    items={[
                      {
                        key: "id",
                        label: t("sidePanel.identifier"),
                        children: (
                          <Text code copyable={{ text: data.Id }} style={{ wordBreak: "break-all" }}>
                            {data.Id.substring(0, 12)}
                          </Text>
                        ),
                      },
                      {
                        key: "image",
                        label: t("sidePanel.image"),
                        children: (
                          <Flex vertical gap={4}>
                            <Text copyable style={{ wordBreak: "break-all" }}>
                              {data.Config.Image}
                            </Text>
                            <Text
                              type="secondary"
                              copyable={{ text: data.Image }}
                              ellipsis={{ tooltip: data.Image }}
                              style={{ fontSize: token.fontSizeSM, maxWidth: "100%" }}
                            >
                              {data.Image}
                            </Text>
                          </Flex>
                        ),
                      },
                      {
                        key: "started",
                        label: t("sidePanel.startedAt.title"),
                        children: summary.startedAt
                          ? formatDate(summary.startedAt)
                          : "—",
                      },
                      {
                        key: "created",
                        label: t("sidePanel.created"),
                        children: summary.created
                          ? formatDate(summary.created)
                          : "—",
                      },
                      {
                        key: "hosts",
                        label: t("sidePanel.hostPath"),
                        children: (
                          <Text copyable style={{ wordBreak: "break-all" }}>
                            {data.HostsPath}
                          </Text>
                        ),
                      },
                      {
                        key: "logPath",
                        label: t("sidePanel.logPath"),
                        children: (
                          <Flex align="center" gap={8} wrap>
                            <Text copyable style={{ wordBreak: "break-all" }}>
                              {data.LogPath}
                            </Text>
                            <Link target="_blank" to={`${appConfig.apiUrl}/logs/${id}`}>
                              <Button size="small" icon={<DownloadOutlined />}>
                                {tc("actions.download")}
                              </Button>
                            </Link>
                          </Flex>
                        ),
                      },
                    ]}
                  />
                ),
              },
              {
                key: "network",
                label: t("sidePanel.network"),
                icon: <ApiOutlined />,
                children: (
                  <Flex vertical gap={16}>
                    {summary.networks.length > 0 && (
                      <Descriptions
                        bordered
                        size="small"
                        column={1}
                        title={t("sidePanel.networks")}
                        items={summary.networks.map(([net, settings]) => ({
                          key: net,
                          label: <Text strong>{net}</Text>,
                          children: settings.IPAddress ? (
                            <Text code copyable>
                              {settings.IPAddress}
                            </Text>
                          ) : (
                            "—"
                          ),
                        }))}
                      />
                    )}
                    <div>
                      <Title level={5}>{t("sidePanel.summary.ports")}</Title>
                      <NetworkTable ports={data.NetworkSettings.Ports ?? {}} />
                    </div>
                  </Flex>
                ),
              },
              {
                key: "mounts",
                label: t("sidePanel.summary.mounts"),
                icon: <FolderOutlined />,
                children:
                  !data.Mounts?.length ? (
                    <Empty description={t("sidePanel.none")} />
                  ) : (
                    <MountTable item={data.Mounts} />
                  ),
              },
              {
                key: "logs",
                label: t("sidePanel.tabs.logs"),
                icon: <ProfileOutlined />,
                children: id && (
                  <FillHeight layoutKey={`${fullSize}`}>
                    {(height) => <LogViewer id={id} height={height} />}
                  </FillHeight>
                ),
              },
              {
                key: "terminal",
                label: t("sidePanel.tabs.terminal"),
                icon: <CodeOutlined />,
                children: terminal,
              },
            ]}
          />
        </Flex>
      )}
    </Drawer>
  );
}
