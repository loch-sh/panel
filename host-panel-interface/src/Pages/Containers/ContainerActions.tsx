import {
  BugFilled,
  CloseSquareFilled,
  CodeTwoTone,
  DeleteOutlined,
  InfoCircleTwoTone,
  InteractionTwoTone,
  PlaySquareTwoTone,
} from "@ant-design/icons";
import { Button, Flex, Modal, Space, Tooltip } from "antd";
import type { ContainerInfo } from "dockerode";
import { ContainerActionEnum } from "../../Services/Containers/Models/containers.model";
import { useCallback, useMemo, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { displayContainerName } from "../../Utils/hostedStack";

interface ContainerActionsProps {
  /** Only the id, state and names are used (hosting pages pass a subset). */
  container: Pick<ContainerInfo, "Id" | "State" | "Names">;
  isTransiting: boolean;
  onAction: (id: string, action: ContainerActionEnum) => void;
  onOpenDrawer: (id: string) => void;
}

interface ActionDef {
  key: string;
  title: string;
  icon: ReactNode;
  onClick: () => void;
  danger?: boolean;
  disabled?: boolean;
  visible?: boolean;
}

function renderActions(actions: ActionDef[]) {
  return actions
    .filter((a) => a.visible !== false)
    .map(({ key, title, icon, onClick, danger, disabled }) => (
      <Tooltip key={key} title={title}>
        <Button
          shape="circle"
          icon={icon}
          onClick={onClick}
          danger={danger}
          disabled={disabled}
        />
      </Tooltip>
    ));
}

export default function ContainerActions({
  container,
  isTransiting,
  onAction,
  onOpenDrawer,
}: ContainerActionsProps) {
  const { Id: id, State: state } = container;
  const isRunning = state === "running";
  const isExited = state === "exited";
  const containerName = displayContainerName(
    container.Names?.[0]?.replace(/^\//, "") ?? id.slice(0, 12),
  );
  const { t } = useTranslation("containers");

  const confirmAction = useCallback(
    (action: ContainerActionEnum, title: string, content: string) => {
      Modal.confirm({
        title,
        content,
        okText: title,
        okButtonProps: { danger: true },
        cancelText: t("actions.cancel", { ns: "common" }),
        onOk: () => onAction(id, action),
      });
    },
    [id, onAction, t],
  );

  const openTerminal = useCallback(() => {
    window.open(
      `/terminal/${id}?name=${encodeURIComponent(containerName)}`,
      `terminal-${id}`,
      "width=900,height=600,menubar=no,toolbar=no,location=no,status=no",
    );
  }, [containerName, id]);

  const dangerActions = useMemo<ActionDef[]>(
    () => [
      {
        key: "delete",
        title: t("actions.delete"),
        icon: <DeleteOutlined />,
        onClick: () =>
          confirmAction(
            ContainerActionEnum.DELETE,
            t("actions.delete"),
            t("confirm.delete", { name: containerName }),
          ),
        danger: true,
        disabled: isTransiting,
        visible: isExited,
      },
      {
        key: "stop",
        title: t("actions.stop"),
        icon: <CloseSquareFilled />,
        onClick: () =>
          confirmAction(
            ContainerActionEnum.STOP,
            t("actions.stop"),
            t("confirm.stop", { name: containerName }),
          ),
        danger: true,
        disabled: isTransiting,
        visible: !isExited,
      },
      {
        key: "kill",
        title: t("actions.kill"),
        icon: <BugFilled />,
        onClick: () =>
          confirmAction(
            ContainerActionEnum.KILL,
            t("actions.kill"),
            t("confirm.kill", { name: containerName }),
          ),
        danger: true,
        disabled: isTransiting,
        visible: !isExited,
      },
    ],
    [containerName, confirmAction, isExited, isTransiting, t],
  );

  const mainActions = useMemo<ActionDef[]>(
    () => [
      {
        key: "start",
        title: t("actions.start"),
        icon: <PlaySquareTwoTone />,
        onClick: () => onAction(id, ContainerActionEnum.START),
        disabled: isTransiting,
        visible: !isRunning,
      },
      {
        key: "restart",
        title: t("actions.restart"),
        icon: <InteractionTwoTone />,
        onClick: () => onAction(id, ContainerActionEnum.RESTART),
        disabled: isTransiting,
        visible: isRunning,
      },
      {
        key: "terminal",
        title: t("actions.detachedTerminal"),
        icon: <CodeTwoTone />,
        onClick: openTerminal,
        visible: isRunning,
      },
      {
        key: "detail",
        title: t("actions.detailsAndTerminal"),
        icon: <InfoCircleTwoTone />,
        onClick: () => onOpenDrawer(id),
      },
    ],
    [id, isRunning, isTransiting, onAction, onOpenDrawer, openTerminal, t],
  );

  return (
    <Flex gap="middle" justify="flex-end">
      {renderActions(dangerActions)}
      <Space.Compact>{renderActions(mainActions)}</Space.Compact>
    </Flex>
  );
}
