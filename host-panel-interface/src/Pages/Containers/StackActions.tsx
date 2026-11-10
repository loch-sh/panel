import {
  CloseSquareFilled,
  InteractionTwoTone,
  PlaySquareTwoTone,
} from "@ant-design/icons";
import { Button, Flex, Modal, Space, Tooltip } from "antd";
import { useCallback } from "react";
import { useTranslation } from "react-i18next";
import { StackActionEnum } from "../../Services/Containers/Models/containers.model";

interface StackActionsProps {
  /** real compose project, sent to the API */
  stack: string;
  /** name shown in confirmations */
  displayName: string;
  runningCount: number;
  totalCount: number;
  /** Disables every button (another stack or container action is running). */
  disabled: boolean;
  /** Shows a spinner on this stack's buttons. */
  loading: boolean;
  onAction: (stack: string, action: StackActionEnum) => void;
}

export default function StackActions({
  stack,
  displayName,
  runningCount,
  totalCount,
  disabled,
  loading,
  onAction,
}: StackActionsProps) {
  const { t } = useTranslation("containers");
  const hasRunning = runningCount > 0;
  const allRunning = runningCount === totalCount;

  const confirmAction = useCallback(
    (action: StackActionEnum, title: string, content: string) => {
      Modal.confirm({
        title,
        content,
        okText: title,
        okButtonProps: { danger: true },
        cancelText: t("actions.cancel", { ns: "common" }),
        onOk: () => onAction(stack, action),
      });
    },
    [onAction, stack, t],
  );

  return (
    <Flex gap="middle" justify="flex-end">
      {hasRunning && (
        <Tooltip title={t("stackActions.stop")}>
          <Button
            shape="circle"
            danger
            icon={<CloseSquareFilled />}
            disabled={disabled}
            loading={loading}
            onClick={() =>
              confirmAction(
                StackActionEnum.STOP,
                t("stackActions.stop"),
                t("confirm.stackStop", { name: displayName }),
              )
            }
          />
        </Tooltip>
      )}
      <Space.Compact>
        {!allRunning && (
          <Tooltip title={t("stackActions.start")}>
            <Button
              shape="circle"
              icon={<PlaySquareTwoTone />}
              disabled={disabled}
              loading={loading}
              onClick={() => onAction(stack, StackActionEnum.START)}
            />
          </Tooltip>
        )}
        {hasRunning && (
          <Tooltip title={t("stackActions.restart")}>
            <Button
              shape="circle"
              icon={<InteractionTwoTone />}
              disabled={disabled}
              loading={loading}
              onClick={() =>
                confirmAction(
                  StackActionEnum.RESTART,
                  t("stackActions.restart"),
                  t("confirm.stackRestart", { name: displayName }),
                )
              }
            />
          </Tooltip>
        )}
      </Space.Compact>
    </Flex>
  );
}
