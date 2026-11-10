import {
  ArrowsAltOutlined,
  CalculatorOutlined,
  CodeOutlined,
  DisconnectOutlined,
  DockerOutlined,
  DownloadOutlined,
  FolderOutlined,
  InfoCircleOutlined,
  InteractionOutlined,
  LoginOutlined,
  PlaySquareOutlined,
  ProfileOutlined,
  ShrinkOutlined,
  WarningOutlined,
} from "@ant-design/icons";
import { Button, Drawer, Flex, List } from "antd";
import Title from "antd/es/typography/Title";
import { useCallback, useMemo, useRef, useState } from "react";
import useContainer from "../../Services/Containers/Hooks/useContainer";
import { format, parseISO } from "date-fns";
import { Trans, useTranslation } from "react-i18next";
import { useDateFnsLocale } from "../../i18n/useLocales";
import LogViewer from "../LogViewer/LogViewer";
import MountTable from "../MountTable/MountTable";
import TerminalInterface, {
  type TerminalHandle,
} from "../Terminal/TerminalInterface";
import NetworkTable from "../NetworkTable/NetworkTable";
import { Link } from "react-router";
import { appConfig } from "../../config";
import { useContainerLabels } from "../../i18n/useContainerLabels";
import {
  displayContainerName,
  displayStackName,
} from "../../Utils/hostedStack";

interface Props {
  open: boolean;
  onClose: () => void;
  id?: string;
}

export default function ContainerSidePanel({
  open,
  onClose,
  id,
}: Readonly<Props>) {
  const [fullSize, setFullSize] = useState(false);
  const [consoleOnly, setConsoleOnly] = useState(false);
  const [terminalNeeded, setTerminalNeeded] = useState(false);
  const [logsNeeded, setLogsNeeded] = useState(false);
  const terminalHandleRef = useRef<TerminalHandle>(null);
  const { t } = useTranslation("containers");
  const { stateLabel } = useContainerLabels();
  const dateLocale = useDateFnsLocale();

  const { isLoading, data } = useContainer({ id });

  const terminalAvailable = !!data?.Config.Tty;

  const shouldShowTerminal = useMemo(
    () => data?.State.Running && terminalNeeded && !!id,
    [data, terminalNeeded, id]
  );

  const handleClose = useCallback(() => {
    setFullSize(false);
    setConsoleOnly(false);
    setTerminalNeeded(false);
    setLogsNeeded(false);
    onClose?.();
  }, [onClose]);

  return (
    <Drawer
      closable
      destroyOnHidden
      open={open}
      onClose={handleClose}
      placement="right"
      title={t("sidePanel.title")}
      loading={isLoading}
      size={fullSize ? "100vw" : "large"}
    >
      <Flex style={{ position: "absolute", top: 13, right: 16, gap: 6 }}>
        <Button
          color={fullSize ? "primary" : "default"}
          variant="outlined"
          icon={fullSize ? <ShrinkOutlined /> : <ArrowsAltOutlined />}
          onClick={() => setFullSize((v) => !v)}
        >
          {t("sidePanel.fullScreen")}
        </Button>
        {data?.State.Running && terminalAvailable && (
          <Button
            color={consoleOnly ? "primary" : "default"}
            variant="outlined"
            icon={<CalculatorOutlined />}
            onClick={() => setConsoleOnly((v) => !v)}
          >
            {t("sidePanel.consoleOnly")}
          </Button>
        )}
      </Flex>

      {!data && <p>{t("sidePanel.noData")}</p>}

      {!!data && open && (
        <Flex vertical>
          <Title>{t("sidePanel.heading", {
              name: displayContainerName(data.Name.substring(1)),
            })}</Title>

          <List>
            {!consoleOnly && (
              <>
                <List.Item>
                  <List.Item.Meta
                    title={t("sidePanel.compose.title")}
                    avatar={<DockerOutlined />}
                    description={
                      "com.docker.compose.project" in data.Config.Labels
                        ? t("sidePanel.compose.project", {
                            project: displayStackName(
                              data.Config.Labels["com.docker.compose.project"],
                            ),
                          })
                        : t("sidePanel.compose.no")
                    }
                  />
                </List.Item>

                <List.Item>
                  <List.Item.Meta
                    title={t("sidePanel.identifier")}
                    avatar={<DockerOutlined />}
                    description={id}
                  />
                </List.Item>

                <List.Item>
                  <List.Item.Meta
                    title={t("sidePanel.image")}
                    avatar={<DockerOutlined />}
                    description={
                      <>
                        {data.Config.Image}
                        <br />
                        {data.Image}
                      </>
                    }
                  />
                </List.Item>

                <List.Item>
                  <List.Item.Meta
                    title={t("sidePanel.currentState")}
                    avatar={<InfoCircleOutlined />}
                    description={stateLabel(data.State.Status)}
                  />
                </List.Item>

                <List.Item>
                  <List.Item.Meta
                    title={t("sidePanel.startedAt.title")}
                    avatar={<PlaySquareOutlined />}
                    description={t("sidePanel.startedAt.value", {
                      date: format(parseISO(data.State.StartedAt), "P", {
                        locale: dateLocale,
                      }),
                      time: format(parseISO(data.State.StartedAt), "pp", {
                        locale: dateLocale,
                      }),
                    })}
                  />
                </List.Item>

                <List.Item>
                  <List.Item.Meta
                    title={t("sidePanel.restartCount")}
                    avatar={<InteractionOutlined />}
                    description={<span>{data.RestartCount}</span>}
                  />
                </List.Item>

                <List.Item>
                  <List.Item.Meta
                    title={t("sidePanel.network")}
                    avatar={<LoginOutlined />}
                    description={
                      <NetworkTable ports={data.NetworkSettings.Ports} />
                    }
                  />
                </List.Item>

                <List.Item>
                  <List.Item.Meta
                    title={t("sidePanel.mounts")}
                    avatar={<FolderOutlined />}
                    description={
                      data.Mounts.length === 0 ? (
                        t("sidePanel.none")
                      ) : (
                        <MountTable item={data.Mounts} />
                      )
                    }
                  />
                </List.Item>

                <List.Item>
                  <List.Item.Meta
                    title={t("sidePanel.hostPath")}
                    avatar={<FolderOutlined />}
                    description={
                      <span style={{ wordBreak: "break-all" }}>
                        {data.HostsPath}
                      </span>
                    }
                  />
                </List.Item>

                <List.Item>
                  <List.Item.Meta
                    title={t("sidePanel.logPath")}
                    avatar={<FolderOutlined />}
                    description={
                      <Flex>
                        <span style={{ wordBreak: "break-all" }}>
                          {data.LogPath}
                        </span>
                        <Link
                          target="_blank"
                          to={`${appConfig.apiUrl}/logs/${id}`}
                        >
                          <Button type="text" icon={<DownloadOutlined />}>
                            {t("actions.download", { ns: "common" })}
                          </Button>
                        </Link>
                      </Flex>
                    }
                  />
                </List.Item>
              </>
            )}

            {!logsNeeded && (
              <List.Item>
                <List.Item.Meta
                  title={t("sidePanel.logs.title")}
                  avatar={<CodeOutlined />}
                  description={
                    <Button onClick={() => setLogsNeeded(true)}>
                      {t("sidePanel.logs.show")}
                    </Button>
                  }
                />
              </List.Item>
            )}

            {logsNeeded && id && (
              <List.Item>
                <List.Item.Meta
                  title={t("sidePanel.logs.title")}
                  avatar={<CodeOutlined />}
                  description={
                    <>
                      <Button
                        onClick={() => setLogsNeeded(false)}
                        danger
                        style={{ marginBottom: 8 }}
                      >
                        {t("sidePanel.logs.close")}
                      </Button>
                      <LogViewer id={id} />
                    </>
                  }
                />
              </List.Item>
            )}

            {terminalAvailable && (
              <>
                {data.State.Running && !terminalNeeded && (
                  <List.Item>
                    <List.Item.Meta
                      title={t("sidePanel.terminal.title")}
                      avatar={<ProfileOutlined />}
                      description={
                        <Flex gap={8}>
                          <Button onClick={() => setTerminalNeeded(true)}>
                            {t("sidePanel.terminal.open")}
                          </Button>
                          {id && (
                            <Button
                              icon={<DisconnectOutlined />}
                              onClick={() =>
                                window.open(
                                  `/terminal/${id}?name=${encodeURIComponent(displayContainerName(data.Name.substring(1)))}`,
                                  `terminal-${id}`,
                                  "width=900,height=600,menubar=no,toolbar=no,location=no,status=no"
                                )
                              }
                            >
                              {t("sidePanel.terminal.detach")}
                            </Button>
                          )}
                        </Flex>
                      }
                    />
                  </List.Item>
                )}

                {shouldShowTerminal && id && (
                  <>
                    <List.Item>
                      <List.Item.Meta
                        title={t("sidePanel.terminal.title")}
                        avatar={<ProfileOutlined />}
                        description={
                          <Flex gap={8}>
                            <Button
                              danger
                              size="small"
                              onClick={() => setTerminalNeeded(false)}
                            >
                              {t("actions.close", { ns: "common" })}
                            </Button>
                            <Button
                              icon={<DisconnectOutlined />}
                              size="small"
                              onClick={() => {
                                const content =
                                  terminalHandleRef.current?.getBufferContent();
                                if (content) {
                                  sessionStorage.setItem(
                                    `terminal-history-${id}`,
                                    content
                                  );
                                }
                                setTerminalNeeded(false);
                                window.open(
                                  `/terminal/${id}?name=${encodeURIComponent(displayContainerName(data.Name.substring(1)))}`,
                                  `terminal-${id}`,
                                  "width=900,height=600,menubar=no,toolbar=no,location=no,status=no"
                                );
                              }}
                            >
                              {t("sidePanel.terminal.detachPopup")}
                            </Button>
                          </Flex>
                        }
                      />
                    </List.Item>
                    <div style={{ height: 400 }}>
                      <TerminalInterface ref={terminalHandleRef} id={id} />
                    </div>
                  </>
                )}
              </>
            )}

            {!terminalAvailable && (
              <List.Item>
                <List.Item.Meta
                  title={t("sidePanel.terminal.title")}
                  avatar={<WarningOutlined />}
                  description={
                    <span>
                      <Trans
                        t={t}
                        i18nKey="sidePanel.terminal.unavailable"
                        components={{ code: <code /> }}
                      />
                    </span>
                  }
                />
              </List.Item>
            )}
          </List>
        </Flex>
      )}
    </Drawer>
  );
}
