import {
  CaretRightOutlined,
  DeleteOutlined,
  ExperimentOutlined,
  PauseCircleOutlined,
  ReloadOutlined,
} from "@ant-design/icons";
import { Button, Modal, Space, Tooltip } from "antd";
import { useTranslation } from "react-i18next";
import { useNavigate } from "react-router";
import { toast } from "react-toastify";
import { useFunctionAction } from "../../Services/Functions/Hooks/useFunctionMutations";
import {
  FunctionAction,
  FunctionSummaryModel,
  needsPurge,
} from "../../Services/Functions/Models/functions.model";
import { getApiErrorMessage } from "../../Utils/apiErrors";
import { DeleteFunctionTarget } from "./DeleteFunctionModal";
import { functionPath } from "./functionLabels";

/** Which actions make sense in a state (the API stays the judge). */
export function availableActions(fn: FunctionSummaryModel) {
  const s = fn.state;
  const purge = needsPurge(s);
  return {
    start: s === "stopped" || s === "missing",
    // Stopping goes through the Docker API: it works on a modified one too.
    stop: s === "running" || s === "unverified" || s === "modified",
    restart: s === "running" || s === "unverified" || s === "missing",
    test: !purge && !!fn.deployment,
    purge,
  };
}

interface Props {
  fn: FunctionSummaryModel;
  onDelete: (target: DeleteFunctionTarget) => void;
  /** the detail page: no "test" shortcut (the tab is there) */
  showTest?: boolean;
}

/** Test, start / stop / restart and delete (or purge) a function. */
export default function FunctionActions({
  fn,
  onDelete,
  showTest = true,
}: Readonly<Props>) {
  const { t } = useTranslation("functions");
  const navigate = useNavigate();
  const { mutateAsync: run, isPending } = useFunctionAction();
  const can = availableActions(fn);

  const perform = async (action: FunctionAction) => {
    try {
      await run({ name: fn.name, action });
      toast.success(t(`toasts.${action}`, { name: fn.name }));
    } catch (err) {
      toast.error(getApiErrorMessage(err));
    }
  };
  const confirm = (action: "stop" | "restart") =>
    Modal.confirm({
      title: t(`confirm.${action}.title`, { name: fn.name }),
      content: t(`confirm.${action}.content`),
      okText: t(`actions.${action}`),
      okButtonProps: { danger: action === "stop" },
      cancelText: t("actions.cancel", { ns: "common" }),
      onOk: () => perform(action),
    });

  return (
    <Space.Compact>
      {showTest && (
        <Tooltip title={t("actions.test")}>
          <Button
            aria-label={t("actions.testOf", { name: fn.name })}
            icon={<ExperimentOutlined />}
            disabled={!can.test}
            onClick={() => navigate(functionPath(fn.name, "test"))}
          />
        </Tooltip>
      )}
      {can.start ? (
        <Tooltip title={t("actions.start")}>
          <Button
            aria-label={t("actions.startOf", { name: fn.name })}
            icon={<CaretRightOutlined />}
            color="green"
            variant="outlined"
            disabled={isPending}
            onClick={() => void perform("start")}
          />
        </Tooltip>
      ) : (
        <Tooltip title={t("actions.stop")}>
          <Button
            aria-label={t("actions.stopOf", { name: fn.name })}
            icon={<PauseCircleOutlined />}
            color="orange"
            variant="outlined"
            disabled={isPending || !can.stop}
            onClick={() => confirm("stop")}
          />
        </Tooltip>
      )}
      <Tooltip title={t("actions.restart")}>
        <Button
          aria-label={t("actions.restartOf", { name: fn.name })}
          icon={<ReloadOutlined />}
          disabled={isPending || !can.restart}
          onClick={() => confirm("restart")}
        />
      </Tooltip>
      <Tooltip title={can.purge ? t("actions.purge") : t("actions.delete")}>
        <Button
          danger
          aria-label={
            can.purge
              ? t("actions.purgeOf", { name: fn.name })
              : t("actions.deleteOf", { name: fn.name })
          }
          icon={<DeleteOutlined />}
          disabled={isPending}
          onClick={() => onDelete({ name: fn.name, purge: can.purge })}
        />
      </Tooltip>
    </Space.Compact>
  );
}
