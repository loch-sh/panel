import { EyeOutlined, PlusOutlined } from "@ant-design/icons";
import { Button, Flex, Skeleton, Table, Tooltip, Typography } from "antd";
import { ColumnsType } from "antd/es/table";
import type { TFunction } from "i18next";
import { useCallback, useMemo } from "react";
import { toast } from "react-toastify";
import { useTranslation } from "react-i18next";
import { Link, useNavigate } from "react-router";
import { PanelTitle } from "../../Components/Base/PanelTitle";
import useHostingConfig from "../../Services/Hosting/Hooks/useHostingConfig";
import useHostingOperationWatcher, {
  OperationFinishedHandler,
} from "../../Services/Hosting/Hooks/useHostingOperationWatcher";
import useHostingStacks from "../../Services/Hosting/Hooks/useHostingStacks";
import {
  HostingStackSummary,
  PhpVersionModel,
} from "../../Services/Hosting/Models/hosting.model";
import DomainLinks from "./DomainLinks";
import HostingDisabled from "./HostingDisabled";
import HostingStackActions from "./HostingStackActions";
import HostingStatus from "./HostingStatus";
import PhpVersionTag from "./PhpVersionTag";
import TemplateTag from "./TemplateTag";

function stackPath(name: string) {
  return `/hosting/${encodeURIComponent(name)}`;
}

function getColumns(
  t: TFunction<"hosting">,
  phpVersions: PhpVersionModel[],
  onOpen: (name: string) => void,
): ColumnsType<HostingStackSummary> {
  const deprecated = new Set(
    phpVersions.filter((v) => v.deprecated).map((v) => v.tag),
  );
  return [
    {
      title: t("columns.name"),
      dataIndex: "name",
      sorter: (a, b) => a.name.localeCompare(b.name),
      defaultSortOrder: "ascend",
      render: (name: string) => (
        <Link to={stackPath(name)} style={{ fontWeight: 600 }}>
          {name}
        </Link>
      ),
    },
    {
      title: t("columns.template"),
      dataIndex: "template",
      responsive: ["md"],
      render: (_, record) => <TemplateTag template={record.template} />,
    },
    {
      title: t("columns.phpVersion"),
      dataIndex: "phpVersion",
      responsive: ["lg"],
      render: (version?: string) => (
        <PhpVersionTag
          version={version}
          deprecated={!!version && deprecated.has(version)}
        />
      ),
    },
    {
      title: t("columns.domains"),
      dataIndex: "domains",
      responsive: ["md"],
      render: (_, record) => (
        <DomainLinks domains={record.domains} https={record.https} vertical />
      ),
    },
    {
      title: t("columns.status"),
      dataIndex: "status",
      render: (_, record) => <HostingStatus {...record} />,
    },
    {
      title: t("columns.actions"),
      align: "right",
      render: (_, record) => (
        <Flex gap="small" justify="flex-end">
          <HostingStackActions stack={record} />
          <Tooltip title={t("actions.details")}>
            <Button
              icon={<EyeOutlined />}
              aria-label={t("actions.details")}
              onClick={() => onOpen(record.name)}
            />
          </Tooltip>
        </Flex>
      ),
    },
  ];
}

export default function HostingList() {
  const { t } = useTranslation("hosting");
  const navigate = useNavigate();
  const {
    data: config,
    isLoading: loadingConfig,
    error: configError,
    refetch: refetchConfig,
  } = useHostingConfig();
  const enabled = config?.enabled ?? false;
  const { data: stacks, isLoading } = useHostingStacks(enabled);

  // Operations finishing while the list is shown: refresh and notify.
  const onOperationFinished = useCallback<OperationFinishedHandler>(
    (name, op) => {
      if (!op) toast.success(t("toasts.deleted", { name }));
      else if (op.state === "succeeded") {
        toast.success(t("toasts.operationSucceeded", { name }));
      } else toast.error(t("toasts.operationFailed", { name }));
    },
    [t],
  );
  useHostingOperationWatcher(stacks, onOperationFinished);

  const columns = useMemo(
    () =>
      getColumns(t, config?.phpVersions ?? [], (name) =>
        navigate(stackPath(name)),
      ),
    [t, config?.phpVersions, navigate],
  );

  if (loadingConfig) return <Skeleton />;
  if (!enabled) {
    return (
      <HostingDisabled
        error={configError}
        onRetry={() => void refetchConfig()}
      />
    );
  }

  return (
    <Flex gap="middle" vertical>
      <Flex justify="space-between" align="center">
        <PanelTitle>{t("title")}</PanelTitle>
        <Button
          type="primary"
          icon={<PlusOutlined />}
          onClick={() => navigate("/hosting/new")}
        >
          {t("newStack")}
        </Button>
      </Flex>

      <Table<HostingStackSummary>
        columns={columns}
        dataSource={stacks}
        loading={isLoading}
        rowKey="name"
        scroll={{ x: "max-content" }}
        pagination={false}
        locale={{
          emptyText: (
            <Typography.Text type="secondary">{t("empty")}</Typography.Text>
          ),
        }}
      />
    </Flex>
  );
}
