import {
  CloseSquareFilled,
  CloudSyncOutlined,
  InteractionTwoTone,
  PlaySquareTwoTone,
} from "@ant-design/icons";
import { Button, Flex, Modal, Space, Tooltip } from "antd";
import { useCallback, useState } from "react";
import { useTranslation } from "react-i18next";
import { toast } from "react-toastify";
import useStackAction from "../../Services/Containers/Hooks/useStackAction";
import { StackActionEnum } from "../../Services/Containers/Models/containers.model";
import useHostingActionRefresh from "../../Services/Hosting/Hooks/useHostingActionRefresh";
import useHostingDeploy from "../../Services/Hosting/Hooks/useHostingDeploy";
import { HostingStackSummary } from "../../Services/Hosting/Models/hosting.model";
import { getApiErrorMessage } from "../../Utils/apiErrors";
import RedeployModal from "./RedeployModal";

const SUCCESS_KEYS = {
  [StackActionEnum.START]: "toasts.started",
  [StackActionEnum.STOP]: "toasts.stopped",
  [StackActionEnum.RESTART]: "toasts.restarted",
} as const;

interface Props {
  stack: Pick<
    HostingStackSummary,
    "name" | "project" | "running" | "total" | "operation" | "modified"
  >;
}

/** Start / stop / restart (generic stack actions) and redeploy of a hosted stack. */
export default function HostingStackActions({ stack }: Readonly<Props>) {
  const { t } = useTranslation("hosting");
  const { mutate: stackAction, isPending: actionPending } = useStackAction();
  const { mutate: deploy, isPending: deployPending } = useHostingDeploy();
  const refresh = useHostingActionRefresh();
  const [redeployOpen, setRedeployOpen] = useState(false);

  const busy = stack.operation?.state === "running";
  const disabled = busy || actionPending || deployPending;
  const hasRunning = stack.running > 0;
  const allRunning = stack.total > 0 && stack.running === stack.total;
  const deployed = stack.total > 0;

  const runAction = useCallback(
    (action: StackActionEnum) =>
      stackAction(
        { stack: stack.project, action },
        {
          onSuccess: ({ failed, results }) => {
            // Polls until settled only when every container succeeded: the
            // expected state is never reached otherwise.
            refresh(stack.name, action, failed === 0);
            if (failed > 0) {
              toast.warning(
                t("toasts.partialFailure", {
                  name: stack.name,
                  failed,
                  total: results.length,
                }),
              );
            } else {
              toast.success(t(SUCCESS_KEYS[action], { name: stack.name }));
            }
          },
          onError: (err) => {
            toast.error(getApiErrorMessage(err));
            refresh(stack.name, action, false);
          },
        },
      ),
    [stackAction, refresh, stack.project, stack.name, t],
  );

  const confirmAction = useCallback(
    (action: StackActionEnum.STOP | StackActionEnum.RESTART) => {
      const title = t(`actions.${action}`);
      Modal.confirm({
        title,
        content: t(`confirm.${action}`, { name: stack.name }),
        okText: title,
        okButtonProps: { danger: true },
        cancelText: t("actions.cancel", { ns: "common" }),
        onOk: () => runAction(action),
      });
    },
    [runAction, stack.name, t],
  );

  const handleRedeploy = useCallback(
    (pull: boolean) =>
      deploy(
        // The modal asked for an explicit confirmation when modified.
        { name: stack.name, pull, force: stack.modified },
        {
          onSuccess: () => {
            toast.info(t("toasts.deployStarted", { name: stack.name }));
            setRedeployOpen(false);
          },
          onError: (err) => toast.error(getApiErrorMessage(err)),
        },
      ),
    [deploy, stack.name, stack.modified, t],
  );

  return (
    <Flex gap="small" justify="flex-end" wrap>
      <Tooltip title={busy ? t("actions.busy") : undefined}>
        <Space.Compact>
          {deployed && !allRunning && (
            <Tooltip title={t("actions.start")}>
              <Button
                icon={<PlaySquareTwoTone />}
                aria-label={t("actions.start")}
                disabled={disabled}
                loading={actionPending}
                onClick={() => runAction(StackActionEnum.START)}
              />
            </Tooltip>
          )}
          {hasRunning && (
            <Tooltip title={t("actions.restart")}>
              <Button
                icon={<InteractionTwoTone />}
                aria-label={t("actions.restart")}
                disabled={disabled}
                loading={actionPending}
                onClick={() => confirmAction(StackActionEnum.RESTART)}
              />
            </Tooltip>
          )}
          {hasRunning && (
            <Tooltip title={t("actions.stop")}>
              <Button
                danger
                icon={<CloseSquareFilled />}
                aria-label={t("actions.stop")}
                disabled={disabled}
                loading={actionPending}
                onClick={() => confirmAction(StackActionEnum.STOP)}
              />
            </Tooltip>
          )}
          <Tooltip title={t("actions.redeploy")}>
            <Button
              icon={<CloudSyncOutlined />}
              aria-label={t("actions.redeploy")}
              disabled={disabled}
              loading={deployPending}
              onClick={() => setRedeployOpen(true)}
            />
          </Tooltip>
        </Space.Compact>
      </Tooltip>

      <RedeployModal
        open={redeployOpen}
        name={stack.name}
        modified={stack.modified}
        loading={deployPending}
        onConfirm={handleRedeploy}
        onClose={() => setRedeployOpen(false)}
      />
    </Flex>
  );
}
