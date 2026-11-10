import { Card, Flex, Table, Tag, Typography } from "antd";
import { ColumnsType } from "antd/es/table";
import { useCallback, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { Link } from "react-router";
import { toast } from "react-toastify";
import ContainerSidePanel from "../../../../Components/ContainerSidePanel/ContainerSidePanel";
import { useContainerLabels } from "../../../../i18n/useContainerLabels";
import useContainerAction from "../../../../Services/Containers/Hooks/useContainerAction";
import { ContainerActionEnum } from "../../../../Services/Containers/Models/containers.model";
import useHostingActionRefresh from "../../../../Services/Hosting/Hooks/useHostingActionRefresh";
import {
  HostingContainer,
  HostingStackDetail,
} from "../../../../Services/Hosting/Models/hosting.model";
import { getApiErrorMessage } from "../../../../Utils/apiErrors";
import { displayContainerName } from "../../../../Utils/hostedStack";
import ContainerActions from "../../../Containers/ContainerActions";

const { Text } = Typography;

const STATE_COLORS: Record<string, string> = {
  running: "green",
  exited: "red",
  created: "blue",
  paused: "orange",
  restarting: "orange",
  dead: "red",
};

/**
 * Containers of the stack with the same actions as the Containers page
 * (start/stop/restart, terminal, details with logs in the side panel).
 */
export default function ContainersCard({
  stack,
}: Readonly<{ stack: HostingStackDetail }>) {
  const { t } = useTranslation("hosting");
  const labels = useContainerLabels();
  const [openContainer, setOpenContainer] = useState("");
  const refresh = useHostingActionRefresh();
  const { mutateAsync: doAction, isPending } = useContainerAction();

  // The stack is refreshed (and polled until it settles) after an action.
  const handleAction = useCallback(
    (id: string, action: ContainerActionEnum) =>
      doAction({ id, action })
        .then(() => refresh(stack.name, action, true, id))
        .catch((err) => {
          toast.error(getApiErrorMessage(err));
          refresh(stack.name, action, false, id);
        }),
    [doAction, refresh, stack.name],
  );

  const columns = useMemo<ColumnsType<HostingContainer>>(
    () => [
      {
        title: t("columns.name"),
        dataIndex: "name",
        // Opens the Containers page on this hosting's stack only.
        render: (name: string) => (
          <Link to={`/containers?stack=${encodeURIComponent(stack.project)}`}>
            <strong>{displayContainerName(name.replace(/^\//, ""))}</strong>
          </Link>
        ),
      },
      {
        title: t("columns.status"),
        dataIndex: "state",
        render: (state: string, record) => (
          <Flex gap={8} align="center" wrap>
            <Tag color={STATE_COLORS[state] ?? "default"}>
              {labels.stateLabel(state)}
            </Tag>
            <Text type="secondary">{labels.statusLabel(record.status)}</Text>
          </Flex>
        ),
      },
      {
        title: t("detail.overview.image"),
        dataIndex: "image",
        responsive: ["md"],
        render: (image: string) => <Text code>{image}</Text>,
      },
      {
        title: t("columns.actions"),
        align: "right",
        render: (_, record) => (
          <Flex justify="end">
            <ContainerActions
              container={{
                Id: record.id,
                State: record.state,
                Names: [record.name],
              }}
              isTransiting={isPending}
              onAction={handleAction}
              onOpenDrawer={setOpenContainer}
            />
          </Flex>
        ),
      },
    ],
    [labels, t, isPending, handleAction, stack.project],
  );

  return (
    <>
      <Card size="small" title={t("detail.overview.containers")}>
        <Table<HostingContainer>
          columns={columns}
          dataSource={stack.containers ?? []}
          rowKey="id"
          size="small"
          pagination={false}
          scroll={{ x: "max-content" }}
          locale={{ emptyText: t("detail.overview.noContainers") }}
        />
      </Card>

      <ContainerSidePanel
        open={!!openContainer}
        id={openContainer}
        onClose={() => setOpenContainer("")}
      />
    </>
  );
}
