import { DeleteOutlined, PlusOutlined } from "@ant-design/icons";
import {
  Alert,
  Button,
  Empty,
  Flex,
  Result,
  Space,
  Table,
  Tabs,
  Tooltip,
  Typography,
} from "antd";
import { ColumnsType } from "antd/es/table";
import type { TFunction } from "i18next";
import { useMemo } from "react";
import { useTranslation } from "react-i18next";
import { Link, useNavigate, useSearchParams } from "react-router";
import { PanelTitle } from "../../Components/Base/PanelTitle";
import useNetworks from "../../Services/Networks/Hooks/useNetworks";
import { NetworkSummaryModel } from "../../Services/Networks/Models/networks.model";
import useMe from "../../Services/Users/Hooks/useMe";
import { UserProfile } from "../../Services/Users/Models/users.model";
import { getApiErrorMessage } from "../../Utils/apiErrors";
import DockerNetworksTable from "./DockerNetworksTable";
import { NETWORKS_PATH, networkPath } from "./networkPaths";
import {
  NetworkScopeTag,
  NetworkStateBadge,
  NoInternetTag,
  ReservedOverlapTag,
} from "./NetworkTags";
import useDeleteNetwork from "./useDeleteNetwork";

const { Text } = Typography;

function getColumns(
  t: TFunction<"networks">,
  busy: boolean,
  onDelete: (name: string) => void,
): ColumnsType<NetworkSummaryModel> {
  return [
    {
      title: t("columns.name"),
      dataIndex: "name",
      render: (name: string) => (
        <Link to={networkPath(name)} style={{ fontWeight: 600 }}>
          {name}
        </Link>
      ),
    },
    {
      title: t("columns.subnet"),
      dataIndex: "subnet",
      render: (subnet: string) =>
        subnet ? <Text code>{subnet}</Text> : <Text type="secondary">—</Text>,
    },
    {
      title: t("columns.gateway"),
      dataIndex: "gateway",
      responsive: ["md"],
      render: (gateway?: string) =>
        gateway ? <Text code>{gateway}</Text> : <Text type="secondary">—</Text>,
    },
    {
      title: t("columns.members"),
      dataIndex: "members",
      align: "right",
    },
    {
      title: t("columns.properties"),
      render: (_: unknown, n) => (
        <Space size={4} wrap>
          <NetworkScopeTag system={n.system} />
          <NoInternetTag />
          <NetworkStateBadge state={n.state} problem={n.problem} />
          <ReservedOverlapTag range={n.reservedOverlap} />
        </Space>
      ),
    },
    {
      title: t("columns.description"),
      dataIndex: "description",
      responsive: ["lg"],
      render: (description?: string) =>
        description ? (
          <Text ellipsis={{ tooltip: description }} style={{ maxWidth: 320 }}>
            {description}
          </Text>
        ) : (
          <Text type="secondary">—</Text>
        ),
    },
    {
      title: t("columns.actions"),
      width: 90,
      render: (_: unknown, n) => {
        const hint = n.system
          ? t("delete.systemHint")
          : n.members > 0
            ? t("delete.inUseHint")
            : t("actions.delete", { ns: "common" });
        return (
          <Tooltip title={hint}>
            <Button
              danger
              shape="circle"
              aria-label={t("actions.delete", { ns: "common" })}
              icon={<DeleteOutlined />}
              disabled={busy || n.system}
              onClick={() => onDelete(n.name)}
            />
          </Tooltip>
        );
      },
    },
  ];
}

type ListTab = "private" | "docker";

/** List of the private networks, and of the other Docker networks (admins only). */
export default function NetworksList() {
  const { t } = useTranslation("networks");
  const navigate = useNavigate();
  const [params, setParams] = useSearchParams();
  const tab: ListTab = params.get("tab") === "docker" ? "docker" : "private";
  const { data: me, isLoading: meLoading } = useMe();
  const isAdmin = me?.profile === UserProfile.ADMIN;
  const { data, isLoading, error } = useNetworks(isAdmin);
  const { ask: askDelete, isPending: deleting } = useDeleteNetwork();
  const columns = useMemo(
    () => getColumns(t, deleting, askDelete),
    [t, deleting, askDelete],
  );
  const locale = useMemo(
    () => ({ emptyText: <Empty description={t("empty")} /> }),
    [t],
  );

  if (meLoading) return null;
  if (!isAdmin) return <Result status="403" title={t("forbidden")} />;

  return (
    <Flex vertical gap="middle">
      <Flex justify="space-between" align="center" wrap gap="small">
        <PanelTitle>{t("title")}</PanelTitle>
        <Button
          type="primary"
          icon={<PlusOutlined />}
          onClick={() => navigate(`${NETWORKS_PATH}/new`)}
        >
          {t("newNetwork")}
        </Button>
      </Flex>
      <Tabs
        activeKey={tab}
        onChange={(key) =>
          setParams(key === "docker" ? { tab: "docker" } : {}, {
            replace: true,
          })
        }
        items={[
          {
            key: "private",
            label: t("tabs.private"),
            children: (
              <Flex vertical gap="middle">
                <Typography.Paragraph
                  type="secondary"
                  style={{ marginBottom: 0 }}
                >
                  {t("intro")}
                </Typography.Paragraph>
                {error && (
                  <Alert
                    type="error"
                    showIcon
                    message={getApiErrorMessage(error)}
                  />
                )}
                <Table<NetworkSummaryModel>
                  columns={columns}
                  dataSource={data}
                  loading={isLoading}
                  rowKey="name"
                  pagination={false}
                  scroll={{ x: "max-content" }}
                  locale={locale}
                />
              </Flex>
            ),
          },
          {
            key: "docker",
            label: t("tabs.docker"),
            children: <DockerNetworksTable enabled={tab === "docker"} />,
          },
        ]}
      />
    </Flex>
  );
}
