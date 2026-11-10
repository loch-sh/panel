import { ArrowLeftOutlined } from "@ant-design/icons";
import {
  Alert,
  Card,
  Descriptions,
  Flex,
  Result,
  Skeleton,
  Space,
  Tooltip,
  Typography,
  Button,
} from "antd";
import { isAxiosError } from "axios";
import { useMemo } from "react";
import { useTranslation } from "react-i18next";
import { useNavigate, useParams } from "react-router";
import { PanelTitle } from "../../Components/Base/PanelTitle";
import useServiceInstances from "../../Services/Databases/Hooks/useServiceInstances";
import useServicesConfig from "../../Services/Databases/Hooks/useServicesConfig";
import { ServiceInstanceModel } from "../../Services/Databases/Models/databases.model";
import useDockerNetwork from "../../Services/Networks/Hooks/useDockerNetwork";
import useMe from "../../Services/Users/Hooks/useMe";
import { UserProfile } from "../../Services/Users/Models/users.model";
import { getApiErrorMessage } from "../../Utils/apiErrors";
import { CodeList, InternalTag } from "./DockerNetworksTable";
import MembersTable from "./MembersTable";
import { DOCKER_NETWORKS_PATH } from "./networkPaths";
import { DockerOriginTag, ReservedOverlapTag } from "./NetworkTags";

const { Text } = Typography;

/** A date and time in the UI language ("" when invalid). */
function formatDateTime(value: string, lang: string | undefined): string {
  const d = new Date(value);
  if (Number.isNaN(d.getTime())) return "";
  return new Intl.DateTimeFormat(lang, {
    dateStyle: "medium",
    timeStyle: "short",
  }).format(d);
}

/** A Docker network the panel did not create: information and containers. */
export default function DockerNetworkDetail() {
  const { t, i18n } = useTranslation("networks");
  const { id = "" } = useParams();
  const navigate = useNavigate();
  const { data: me, isLoading: meLoading } = useMe();
  const isAdmin = me?.profile === UserProfile.ADMIN;
  const { data: servicesConfig } = useServicesConfig(isAdmin);
  const { data: instances } = useServiceInstances(
    servicesConfig?.enabled ?? false,
  );
  const { data: network, isLoading, error } = useDockerNetwork(id, isAdmin);
  const instancesByKey = useMemo(
    () =>
      new Map<string, ServiceInstanceModel>(
        (instances ?? []).map((i) => [i.key, i]),
      ),
    [instances],
  );

  if (meLoading) return null;
  if (!isAdmin) return <Result status="403" title={t("forbidden")} />;

  const header = (
    <Flex gap="small" align="center" wrap>
      <Button
        type="text"
        icon={<ArrowLeftOutlined />}
        aria-label={t("docker.back")}
        onClick={() => navigate(DOCKER_NETWORKS_PATH)}
      />
      <PanelTitle>{network?.name ?? t("docker.title")}</PanelTitle>
      {network && (
        <Space size={4} wrap>
          <DockerOriginTag
            origin={network.origin}
            composeProject={network.composeProject}
          />
          <InternalTag internal={network.internal} />
          <ReservedOverlapTag range={network.reservedOverlap} />
        </Space>
      )}
    </Flex>
  );

  if (isLoading) {
    return (
      <Flex vertical gap="middle">
        {header}
        <Skeleton active />
      </Flex>
    );
  }
  if (error || !network) {
    const notFound = isAxiosError(error) && error.response?.status === 404;
    return (
      <Flex vertical gap="middle">
        {header}
        <Alert
          type={notFound ? "warning" : "error"}
          showIcon
          message={notFound ? t("docker.notFound") : getApiErrorMessage(error)}
        />
      </Flex>
    );
  }

  return (
    <Flex vertical gap="middle" style={{ maxWidth: 1400 }}>
      {header}
      <Alert type="info" showIcon message={t("docker.readOnly")} />
      {network.reservedOverlap && (
        <Alert
          type="warning"
          showIcon
          message={t("tags.reservedOverlap")}
          description={t("tagHints.reservedOverlap", {
            range: network.reservedOverlap,
          })}
        />
      )}

      <Card title={t("detail.info")}>
        <Descriptions
          column={{ xs: 1, md: 2 }}
          size="small"
          items={[
            {
              key: "id",
              label: t("detail.id"),
              children: (
                <Tooltip title={network.id}>
                  <Text code copyable={{ text: network.id }}>
                    {network.id.slice(0, 12)}
                  </Text>
                </Tooltip>
              ),
            },
            {
              key: "driver",
              label: t("docker.columns.driver"),
              children: (
                <Text code>
                  {network.scope
                    ? `${network.driver} (${network.scope})`
                    : network.driver}
                </Text>
              ),
            },
            {
              key: "subnet",
              label: t("detail.subnet"),
              children: <CodeList values={network.subnets} />,
            },
            {
              key: "gateway",
              label: t("detail.gateway"),
              children: <CodeList values={network.gateways} />,
            },
            {
              key: "internet",
              label: t("detail.internet"),
              children: network.internal
                ? t("detail.internetNone")
                : t("docker.internetYes"),
            },
            {
              key: "created",
              label: t("detail.created"),
              children:
                formatDateTime(network.createdAt, i18n.resolvedLanguage) || "—",
            },
          ]}
        />
      </Card>

      <Card
        title={t("docker.containersTitle", { count: network.members.length })}
        styles={{ body: { padding: 0 } }}
      >
        <MembersTable
          members={network.members}
          instances={instancesByKey}
          emptyText={t("docker.containersEmpty")}
        />
      </Card>
    </Flex>
  );
}
