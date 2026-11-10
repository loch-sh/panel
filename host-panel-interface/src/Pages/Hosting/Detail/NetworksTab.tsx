import { DisconnectOutlined, PlusOutlined } from "@ant-design/icons";
import { useQueries } from "@tanstack/react-query";
import {
  Alert,
  Badge,
  Button,
  Card,
  Empty,
  Flex,
  Form,
  Modal,
  Select,
  Space,
  Table,
  Tooltip,
  Typography,
} from "antd";
import { ColumnsType } from "antd/es/table";
import type { TFunction } from "i18next";
import { useCallback, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { Link } from "react-router";
import { toast } from "react-toastify";
import {
  HostingSpec,
  HostingStackDetail,
} from "../../../Services/Hosting/Models/hosting.model";
import useDockerNetworks from "../../../Services/Networks/Hooks/useDockerNetworks";
import {
  useNetworkAttach,
  useNetworkDetach,
} from "../../../Services/Networks/Hooks/useNetworkMutations";
import useNetworks from "../../../Services/Networks/Hooks/useNetworks";
import {
  DockerNetworkModel,
  NetworkMemberModel,
  NetworkSummaryModel,
} from "../../../Services/Networks/Models/networks.model";
import {
  networksService,
  networksServiceKeys,
} from "../../../Services/Networks/networks.service";
import { getApiErrorMessage } from "../../../Utils/apiErrors";
import {
  DockerOriginTag,
  NetworkScopeTag,
  NetworkStateBadge,
  ReservedOverlapTag,
} from "../../Networks/NetworkTags";
import {
  NETWORKS_PATH,
  dockerNetworkPath,
  networkPath,
} from "../../Networks/networkPaths";

const { Text } = Typography;

/** At most this many Docker networks are inspected to find the site's own. */
const MAX_DOCKER_LOOKUPS = 20;

/** A panel network declared by the site. */
interface SiteNetworkRow {
  name: string;
  /** undefined: not (or no longer) registered in the panel */
  network?: NetworkSummaryModel;
  /** the site on this network (its address, whether it is connected) */
  member?: NetworkMemberModel;
  loading: boolean;
}

const dash = <Text type="secondary">—</Text>;

function code(value?: string) {
  return value ? <Text code>{value}</Text> : dash;
}

function getColumns(
  t: TFunction<"networks">,
  disabledReason: string | undefined,
  onDisconnect: (name: string) => void,
): ColumnsType<SiteNetworkRow> {
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
      title: t("site.columns.type"),
      render: (_: unknown, r) =>
        r.network ? <NetworkScopeTag system={r.network.system} /> : dash,
    },
    {
      title: t("columns.subnet"),
      render: (_: unknown, r) => code(r.network?.subnet),
    },
    {
      title: t("columns.gateway"),
      responsive: ["md"],
      render: (_: unknown, r) => code(r.network?.gateway),
    },
    {
      title: t("members.columns.ipv4"),
      render: (_: unknown, r) =>
        r.member?.present && r.member.ipv4 ? (
          <Text code copyable={{ text: r.member.ipv4 }}>
            {r.member.ipv4}
          </Text>
        ) : (
          dash
        ),
    },
    {
      title: t("site.columns.state"),
      render: (_: unknown, r) => {
        if (r.loading) return dash;
        if (!r.network) {
          return (
            <Tooltip title={t("site.unknownHint")}>
              <Badge status="error" text={t("site.unknown")} />
            </Tooltip>
          );
        }
        return r.member?.present ? (
          <Tooltip title={t("site.connectedHint")}>
            <Badge status="success" text={t("site.connected")} />
          </Tooltip>
        ) : (
          <Tooltip title={t("members.notConnectedHint")}>
            <Badge status="warning" text={t("members.notConnected")} />
          </Tooltip>
        );
      },
    },
    {
      title: t("site.columns.warnings"),
      render: (_: unknown, r) =>
        r.network && (r.network.state !== "ok" || r.network.reservedOverlap) ? (
          <Space size={4} wrap>
            {r.network.state !== "ok" && (
              <NetworkStateBadge
                state={r.network.state}
                problem={r.network.problem}
              />
            )}
            <ReservedOverlapTag range={r.network.reservedOverlap} />
          </Space>
        ) : (
          dash
        ),
    },
    {
      title: t("columns.actions"),
      width: 130,
      render: (_: unknown, r) => {
        // An unknown network cannot be detached through the networks API.
        const reason =
          disabledReason ?? (r.network ? undefined : t("site.unknownHint"));
        return (
          <Tooltip title={reason}>
            <Button
              size="small"
              danger
              icon={<DisconnectOutlined />}
              disabled={!!reason}
              onClick={() => onDisconnect(r.name)}
            >
              {t("site.disconnect")}
            </Button>
          </Tooltip>
        );
      },
    },
  ];
}

interface ConnectModalProps {
  open: boolean;
  stack: string;
  networks: readonly NetworkSummaryModel[];
  declared: readonly string[];
  loading: boolean;
  onCancel: () => void;
  onSubmit: (network: string) => void;
}

/** Chooses a panel network the site is not on yet. */
function ConnectNetworkModal({
  open,
  stack,
  networks,
  declared,
  loading,
  onCancel,
  onSubmit,
}: Readonly<ConnectModalProps>) {
  const { t } = useTranslation("networks");
  const [value, setValue] = useState<string>();
  const options = useMemo(
    () =>
      networks
        .filter((n) => !declared.includes(n.name))
        .map((n) => ({
          value: n.name,
          // The API refuses a network that is missing or compromised.
          disabled: n.state !== "ok",
          label: (
            <Space size="small">
              <Text strong>{n.name}</Text>
              {n.subnet && <Text type="secondary">{n.subnet}</Text>}
              {n.state !== "ok" && (
                <Text type="danger">{t(`state.${n.state}`)}</Text>
              )}
            </Space>
          ),
          title: n.name,
        })),
    [networks, declared, t],
  );
  return (
    <Modal
      open={open}
      title={t("site.connectModal.title", { name: stack })}
      okText={t("site.connectModal.submit")}
      cancelText={t("actions.cancel", { ns: "common" })}
      okButtonProps={{ disabled: !value, loading }}
      onCancel={onCancel}
      // Reset once closed, so a failed connect keeps the choice for a retry.
      afterClose={() => setValue(undefined)}
      onOk={() => {
        if (value) onSubmit(value);
      }}
      destroyOnHidden
    >
      {options.length === 0 ? (
        <Empty
          image={Empty.PRESENTED_IMAGE_SIMPLE}
          description={
            <Flex vertical gap={4} align="center">
              <Text type="secondary">{t("site.connectModal.none")}</Text>
              <Link to={`${NETWORKS_PATH}/new`}>{t("select.createLink")}</Link>
            </Flex>
          }
        />
      ) : (
        <Flex vertical gap="middle">
          <Form layout="vertical">
            <Form.Item
              label={t("site.connectModal.network")}
              htmlFor="connect-network"
              style={{ marginBottom: 0 }}
            >
              <Select
                id="connect-network"
                showSearch={{ optionFilterProp: "title" }}
                value={value}
                onChange={setValue}
                options={options}
                optionLabelProp="title"
                placeholder={t("site.connectModal.placeholder")}
              />
            </Form.Item>
          </Form>
          <Alert
            type="warning"
            showIcon
            message={t("attach.restart.site", { name: stack })}
          />
        </Flex>
      )}
    </Modal>
  );
}

/**
 * Docker networks the panel does not manage that the site's container is on
 * (e.g. the reverse proxy network), read-only.
 */
function OtherDockerNetworks({ stack }: Readonly<{ stack: string }>) {
  const { t } = useTranslation("networks");
  const { data: list, isLoading, error } = useDockerNetworks();
  // Only networks with containers can hold the site's.
  const candidates = useMemo(
    () =>
      (list ?? []).filter((n) => n.containers > 0).slice(0, MAX_DOCKER_LOOKUPS),
    [list],
  );
  const details = useQueries({
    queries: candidates.map((n) => ({
      queryKey: [networksServiceKeys.dockerNetwork, n.id],
      queryFn: () => networksService.getDocker(n.id),
      staleTime: 30_000,
      retry: false,
    })),
  });
  const loading = isLoading || details.some((d) => d.isLoading);
  const rows = candidates.flatMap((n, i) => {
    const member = details[i]?.data?.members.find(
      (m) => m.kind === "site" && m.name === stack,
    );
    return member ? [{ network: n, member }] : [];
  });

  const columns: ColumnsType<{
    network: DockerNetworkModel;
    member: NetworkMemberModel;
  }> = [
    {
      title: t("columns.name"),
      render: (_: unknown, r) => (
        <Link to={dockerNetworkPath(r.network.id)} style={{ fontWeight: 600 }}>
          {r.network.name}
        </Link>
      ),
    },
    {
      title: t("docker.columns.origin"),
      render: (_: unknown, r) => (
        <DockerOriginTag
          origin={r.network.origin}
          composeProject={r.network.composeProject}
        />
      ),
    },
    {
      title: t("columns.subnet"),
      render: (_: unknown, r) =>
        r.network.subnets.length > 0 ? (
          <Text code>{r.network.subnets.join(", ")}</Text>
        ) : (
          dash
        ),
    },
    {
      title: t("members.columns.ipv4"),
      render: (_: unknown, r) => code(r.member.ipv4),
    },
    {
      title: t("site.columns.warnings"),
      render: (_: unknown, r) =>
        r.network.reservedOverlap ? (
          <ReservedOverlapTag range={r.network.reservedOverlap} />
        ) : (
          dash
        ),
    },
  ];

  return (
    <Card title={t("site.other.title")} styles={{ body: { padding: 0 } }}>
      <Flex vertical>
        <Text type="secondary" style={{ padding: "12px 16px" }}>
          {t("site.other.intro")}
        </Text>
        {error && (
          <Alert
            type="error"
            showIcon
            banner
            message={t("site.other.loadFailed")}
          />
        )}
        <Table
          columns={columns}
          dataSource={rows}
          loading={loading}
          rowKey={(r) => r.network.id}
          pagination={false}
          size="middle"
          scroll={{ x: "max-content" }}
          locale={{ emptyText: t("site.other.empty") }}
        />
      </Flex>
    </Card>
  );
}

interface Props {
  stack: HostingStackDetail & { spec: HostingSpec };
  /** the site is being redeployed (operation to follow) */
  onOperationStarted: (operationId: string) => void;
}

/**
 * Networks tab of a hosted site: the panel networks it is on (address,
 * state), connect / disconnect (redeploys the site), and the other Docker
 * networks its container is on.
 */
export default function NetworksTab({
  stack,
  onOperationStarted,
}: Readonly<Props>) {
  const { t } = useTranslation("networks");
  const busy = stack.operation?.state === "running";
  const declared = useMemo(
    () => stack.spec.networks ?? [],
    [stack.spec.networks],
  );
  const { data: networks, error: listError } = useNetworks();
  const details = useQueries({
    queries: declared.map((name) => ({
      queryKey: [networksServiceKeys.network, name],
      queryFn: () => networksService.get(name),
      refetchInterval: busy ? 2_000 : 15_000,
      retry: false,
    })),
  });
  const { mutateAsync: attach, isPending: attaching } = useNetworkAttach();
  const { mutateAsync: detach, isPending: detaching } = useNetworkDetach();
  const [connectOpen, setConnectOpen] = useState(false);

  const rows: SiteNetworkRow[] = declared.map((name, i) => ({
    name,
    network: networks?.find((n) => n.name === name),
    member: details[i]?.data?.members.find(
      (m) => m.kind === "site" && m.name === stack.name,
    ),
    loading: !networks || !!details[i]?.isLoading,
  }));

  const disabledReason = stack.modified
    ? t("site.modifiedHint")
    : busy
      ? t("site.busyHint")
      : attaching || detaching
        ? t("site.busyHint")
        : undefined;

  const started = useCallback(
    (operation: string | undefined, connected: boolean, network: string) => {
      if (operation) {
        onOperationStarted(operation);
        toast.info(t("toasts.siteRedeploying", { name: stack.name }));
      } else {
        toast.success(
          t(connected ? "site.toasts.connected" : "site.toasts.disconnected", {
            name: stack.name,
            network,
          }),
        );
      }
    },
    [onOperationStarted, stack.name, t],
  );

  const onConnect = async (network: string) => {
    try {
      const res = await attach({ network, kind: "site", name: stack.name });
      setConnectOpen(false);
      started(res.operation, true, network);
    } catch (err) {
      toast.error(getApiErrorMessage(err));
    }
  };

  const onDisconnect = useCallback(
    (network: string) => {
      Modal.confirm({
        title: t("members.detachConfirm.title", {
          name: stack.name,
          network,
        }),
        content: t("members.detachConfirm.site"),
        okText: t("site.disconnect"),
        okButtonProps: { danger: true },
        cancelText: t("actions.cancel", { ns: "common" }),
        onOk: async () => {
          try {
            const res = await detach({
              network,
              kind: "site",
              name: stack.name,
            });
            started(res.operation, false, network);
          } catch (err) {
            toast.error(getApiErrorMessage(err));
          }
        },
      });
    },
    [detach, stack.name, started, t],
  );

  const columns = useMemo(
    () => getColumns(t, disabledReason, onDisconnect),
    [t, disabledReason, onDisconnect],
  );

  return (
    <Flex vertical gap="middle">
      {stack.modified && (
        <Alert type="warning" showIcon message={t("site.modifiedHint")} />
      )}
      <Card
        title={t("site.title")}
        styles={{ body: { padding: 0 } }}
        extra={
          <Tooltip title={disabledReason}>
            <Button
              type="primary"
              icon={<PlusOutlined />}
              disabled={!!disabledReason || !networks}
              loading={attaching}
              onClick={() => setConnectOpen(true)}
            >
              {t("site.connect")}
            </Button>
          </Tooltip>
        }
      >
        <Flex vertical>
          <Text type="secondary" style={{ padding: "12px 16px" }}>
            {t("site.intro")}
          </Text>
          {listError && (
            <Alert
              type="error"
              showIcon
              banner
              message={getApiErrorMessage(listError)}
            />
          )}
          <Table<SiteNetworkRow>
            columns={columns}
            dataSource={rows}
            rowKey="name"
            pagination={false}
            size="middle"
            scroll={{ x: "max-content" }}
            locale={{ emptyText: t("site.empty") }}
          />
        </Flex>
      </Card>

      <OtherDockerNetworks stack={stack.name} />

      <ConnectNetworkModal
        open={connectOpen}
        stack={stack.name}
        networks={networks ?? []}
        declared={declared}
        loading={attaching}
        onCancel={() => setConnectOpen(false)}
        onSubmit={(n) => void onConnect(n)}
      />
    </Flex>
  );
}
