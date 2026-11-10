import {
  Alert,
  Empty,
  Flex,
  Space,
  Table,
  Tag,
  Tooltip,
  Typography,
} from "antd";
import { ColumnsType } from "antd/es/table";
import type { TFunction } from "i18next";
import { useMemo } from "react";
import { useTranslation } from "react-i18next";
import { Link } from "react-router";
import useDockerNetworks from "../../Services/Networks/Hooks/useDockerNetworks";
import { DockerNetworkModel } from "../../Services/Networks/Models/networks.model";
import { getApiErrorMessage } from "../../Utils/apiErrors";
import { dockerNetworkPath } from "./networkPaths";
import { DockerOriginTag, ReservedOverlapTag } from "./NetworkTags";

const { Text } = Typography;

/** Addresses as code, "—" when none. */
export function CodeList({ values }: Readonly<{ values: readonly string[] }>) {
  if (values.length === 0) return <Text type="secondary">—</Text>;
  return (
    <Space size={4} wrap>
      {values.map((v) => (
        <Text key={v} code>
          {v}
        </Text>
      ))}
    </Space>
  );
}

/** Internal (no Internet access) or not. */
export function InternalTag({ internal }: Readonly<{ internal: boolean }>) {
  const { t } = useTranslation("networks");
  return internal ? (
    <Tooltip title={t("tagHints.noInternet")}>
      <Tag>{t("tags.noInternet")}</Tag>
    </Tooltip>
  ) : (
    <Tooltip title={t("docker.internetHint")}>
      <Tag color="default">{t("docker.internet")}</Tag>
    </Tooltip>
  );
}

function getColumns(t: TFunction<"networks">): ColumnsType<DockerNetworkModel> {
  return [
    {
      title: t("columns.name"),
      dataIndex: "name",
      render: (name: string, n) => (
        <Link to={dockerNetworkPath(n.id)} style={{ fontWeight: 600 }}>
          {name}
        </Link>
      ),
    },
    {
      title: t("docker.columns.origin"),
      dataIndex: "origin",
      render: (_: unknown, n) => (
        <DockerOriginTag origin={n.origin} composeProject={n.composeProject} />
      ),
    },
    {
      title: t("docker.columns.driver"),
      dataIndex: "driver",
      responsive: ["md"],
      render: (driver: string) => <Text code>{driver}</Text>,
    },
    {
      title: t("columns.subnet"),
      dataIndex: "subnets",
      render: (subnets: string[]) => <CodeList values={subnets} />,
    },
    {
      title: t("columns.gateway"),
      dataIndex: "gateways",
      responsive: ["md"],
      render: (gateways: string[]) => <CodeList values={gateways} />,
    },
    {
      title: t("docker.columns.containers"),
      dataIndex: "containers",
      align: "right",
    },
    {
      title: t("columns.properties"),
      render: (_: unknown, n) => (
        <Space size={4} wrap>
          <InternalTag internal={n.internal} />
          <ReservedOverlapTag range={n.reservedOverlap} />
        </Space>
      ),
    },
  ];
}

/** Docker networks the panel did not create (read-only). */
export default function DockerNetworksTable({
  enabled,
}: Readonly<{ enabled: boolean }>) {
  const { t } = useTranslation("networks");
  const { data, isLoading, error } = useDockerNetworks(enabled);
  const columns = useMemo(() => getColumns(t), [t]);
  const locale = useMemo(
    () => ({ emptyText: <Empty description={t("docker.empty")} /> }),
    [t],
  );
  return (
    <Flex vertical gap="middle">
      <Typography.Paragraph type="secondary" style={{ marginBottom: 0 }}>
        {t("docker.intro")}
      </Typography.Paragraph>
      {error && (
        <Alert type="error" showIcon message={getApiErrorMessage(error)} />
      )}
      <Table<DockerNetworkModel>
        columns={columns}
        dataSource={data}
        loading={isLoading}
        rowKey="id"
        pagination={false}
        scroll={{ x: "max-content" }}
        locale={locale}
      />
    </Flex>
  );
}
