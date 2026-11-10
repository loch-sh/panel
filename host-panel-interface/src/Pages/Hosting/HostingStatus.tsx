import {
  CloseCircleOutlined,
  EditOutlined,
  LoadingOutlined,
} from "@ant-design/icons";
import { Badge, Space, Spin, Tag, Tooltip } from "antd";
import { useTranslation } from "react-i18next";
import useIsSettling from "../../Services/Hosting/Hooks/useIsSettling";
import {
  HostingStackStatus,
  HostingStackSummary,
} from "../../Services/Hosting/Models/hosting.model";

const BADGE_STATUS = {
  running: "success",
  partial: "warning",
  stopped: "error",
  not_deployed: "default",
} as const satisfies Record<
  HostingStackStatus,
  "success" | "warning" | "error" | "default"
>;

type Props = Pick<
  HostingStackSummary,
  "name" | "status" | "running" | "total" | "modified" | "operation"
>;

/** Stack status badge, with the running operation, manual edits and failures. */
export default function HostingStatus({
  name,
  status,
  running,
  total,
  modified,
  operation,
}: Readonly<Props>) {
  const { t } = useTranslation("hosting");
  const label = t(`status.${status}`);
  // The containers are still changing state after a start/stop/restart.
  const settling = useIsSettling(name);
  return (
    <Space size={4} wrap>
      <Badge
        status={BADGE_STATUS[status] ?? "default"}
        text={
          total > 0 ? t("status.withCount", { label, running, total }) : label
        }
      />
      {settling && (
        <Tooltip title={t("status.refreshing")}>
          <Spin
            size="small"
            indicator={<LoadingOutlined />}
            aria-label={t("status.refreshing")}
          />
        </Tooltip>
      )}
      {operation?.state === "running" && (
        <Tag icon={<LoadingOutlined />} color="processing">
          {t(`operation.kind.${operation.kind}`)}
        </Tag>
      )}
      {operation?.state === "failed" && (
        <Tag icon={<CloseCircleOutlined />} color="error">
          {t("status.lastOperationFailed")}
        </Tag>
      )}
      {modified && (
        <Tooltip title={t("status.modifiedTooltip")}>
          <Tag icon={<EditOutlined />} color="warning">
            {t("status.modified")}
          </Tag>
        </Tooltip>
      )}
    </Space>
  );
}
