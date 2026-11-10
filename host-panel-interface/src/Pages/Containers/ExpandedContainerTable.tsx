import { CopyTwoTone } from "@ant-design/icons";
import { Button, Flex, Table, Tooltip } from "antd";
import { ColumnsType } from "antd/es/table";
import { webRoutes } from "../../Utils/webRouting";
import { ContainerInfo } from "dockerode";
import { useMemo } from "react";
import { toast } from "react-toastify";
import type { TFunction } from "i18next";
import { useTranslation } from "react-i18next";
import { ContainerActionEnum } from "../../Services/Containers/Models/containers.model";
import ContainerActions from "./ContainerActions";
import { useContainerLabels } from "../../i18n/useContainerLabels";

interface ExpandedContainerTableProps {
  containers: ContainerInfo[];
  isTransiting: boolean;
  onAction: (id: string, action: ContainerActionEnum) => void;
  onOpenDrawer: (id: string) => void;
}

function getColumns(props: {
  t: TFunction<"containers">;
  labels: ReturnType<typeof useContainerLabels>;
  isTransiting: boolean;
  onAction: (id: string, action: ContainerActionEnum) => void;
  onOpenDrawer: (id: string) => void;
}): ColumnsType<ContainerInfo> {
  const { t, labels } = props;
  return [
    {
      title: t("columns.id"),
      dataIndex: "Id",
      responsive: ["md"],
      render: (value) => (
        <Tooltip title={t("table.copyIdTooltip")}>
          <Button
            color="default"
            variant="text"
            icon={<CopyTwoTone />}
            onClick={() => {
              navigator.clipboard.writeText(value).then(() =>
                toast(t("table.idCopied"), {
                  type: "success",
                  closeOnClick: true,
                }),
              );
            }}
          >
            {value.substring(0, 12)}
          </Button>
        </Tooltip>
      ),
    },
    {
      title: t("columns.name"),
      dataIndex: "Names",
      render: (value: string[], row) => {
        let name = value[0];
        if (row.Labels?.["com.docker.compose.project"]) {
          name = name.replace(
            `${row.Labels["com.docker.compose.project"]}-`,
            "",
          );
        }
        const isErrorExit =
          row.State === "exited" && !row.Status.includes("(0)");
        return (
          <span style={{ fontWeight: 500 }}>
            {name.substring(1)}
            {isErrorExit && (
              <Tooltip title={t("table.abnormalExit")}>
                <span className="error-pulse" style={{ cursor: "help" }}>
                  {" "}
                  ⚠️
                </span>
              </Tooltip>
            )}
          </span>
        );
      },
    },
    {
      title: t("columns.state"),
      dataIndex: "State",
      responsive: ["md"],
      render: (_: string, record: ContainerInfo) => {
        const status = labels.statusLabel(record.Status);
        // A recognized status already includes the state ("Exited (0) 5 days ago")
        return status !== record.Status ? (
          <span>{status}</span>
        ) : (
          <span>
            {labels.stateLabel(record.State)},<br /> {record.Status}
          </span>
        );
      },
    },
    {
      title: t("columns.webRouting"),
      responsive: ["md"],
      render: (_: string, record: ContainerInfo) => {
        const routes = webRoutes(record.Labels);
        if (routes.length === 0) return "";
        return (
          <Flex vertical>
            {routes.map((r) => (
              <a
                key={r.host}
                href={r.url}
                target="_blank"
                rel="noopener noreferrer"
              >
                {r.url}
              </a>
            ))}
          </Flex>
        );
      },
    },
    {
      title: t("columns.actions"),
      dataIndex: "Id",
      render: (_, record) => (
        <ContainerActions
          container={record}
          isTransiting={props.isTransiting}
          onAction={props.onAction}
          onOpenDrawer={props.onOpenDrawer}
        />
      ),
    },
  ];
}

export default function ExpandedContainerTable({
  containers,
  isTransiting,
  onAction,
  onOpenDrawer,
}: ExpandedContainerTableProps) {
  const { t } = useTranslation("containers");
  const labels = useContainerLabels();
  const columns = useMemo(
    () => getColumns({ t, labels, isTransiting, onAction, onOpenDrawer }),
    [t, labels, isTransiting, onAction, onOpenDrawer],
  );

  return (
    <Table<ContainerInfo>
      columns={columns}
      dataSource={containers}
      rowKey="Id"
      pagination={false}
      size="small"
      scroll={{ x: "max-content" }}
    />
  );
}
