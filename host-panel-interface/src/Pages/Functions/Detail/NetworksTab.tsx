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
  Switch,
  Table,
  Tag,
  Tooltip,
  Typography,
} from "antd";
import { ColumnsType } from "antd/es/table";
import { useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { Link } from "react-router";
import { toast } from "react-toastify";
import {
  useFunctionEgress,
  useFunctionNetworks,
} from "../../../Services/Functions/Hooks/useFunctionMutations";
import {
  dedicatedNetwork,
  FunctionDetailModel,
} from "../../../Services/Functions/Models/functions.model";
import useNetworks from "../../../Services/Networks/Hooks/useNetworks";
import {
  NetworkMemberModel,
  NetworkSummaryModel,
  SYSTEM_NETWORK,
} from "../../../Services/Networks/Models/networks.model";
import {
  networksService,
  networksServiceKeys,
} from "../../../Services/Networks/networks.service";
import { getApiErrorMessage } from "../../../Utils/apiErrors";
import { networkPath } from "../../Networks/networkPaths";
import { NetworkScopeTag } from "../../Networks/NetworkTags";

const { Text } = Typography;

interface Row {
  name: string;
  network?: NetworkSummaryModel;
  member?: NetworkMemberModel;
  loading: boolean;
}

const dash = <Text type="secondary">—</Text>;

/** Networks of a function (attach / detach) and its Internet access. */
export default function NetworksTab({
  fn,
}: Readonly<{ fn: FunctionDetailModel }>) {
  const { t } = useTranslation("functions");
  const { t: tn } = useTranslation("networks");
  const { data: networks, isLoading } = useNetworks();
  const { mutateAsync: setNetworks, isPending } = useFunctionNetworks();
  const { mutateAsync: setEgress, isPending: egressPending } =
    useFunctionEgress();
  const [connectOpen, setConnectOpen] = useState(false);
  const [choice, setChoice] = useState<string>();
  const own = dedicatedNetwork(fn.name);

  // The function's address on each network, from the network's members.
  const details = useQueries({
    queries: fn.networks.map((n) => ({
      queryKey: [networksServiceKeys.network, n],
      queryFn: () => networksService.get(n),
      retry: false,
    })),
  });
  const rows = useMemo<Row[]>(
    () =>
      fn.networks.map((name, i) => ({
        name,
        network: networks?.find((n) => n.name === name),
        member: details[i]?.data?.members.find(
          (m) => m.kind === "function" && m.name === fn.name,
        ),
        loading: isLoading || !!details[i]?.isLoading,
      })),
    [fn.networks, fn.name, networks, details, isLoading],
  );
  const available = (networks ?? []).filter(
    (n) =>
      n.name !== SYSTEM_NETWORK &&
      !fn.networks.includes(n.name) &&
      n.state === "ok",
  );

  const apply = async (next: string[], network: string, connected: boolean) => {
    try {
      await setNetworks({ name: fn.name, networks: next });
      toast.success(
        t(connected ? "networks.connected" : "networks.disconnected", {
          name: fn.name,
          network,
        }),
      );
      return true;
    } catch (err) {
      toast.error(getApiErrorMessage(err));
      return false;
    }
  };
  const connect = async () => {
    if (!choice) return;
    if (await apply([...fn.networks, choice], choice, true)) {
      setConnectOpen(false);
      setChoice(undefined);
    }
  };
  const askDisconnect = (network: string) =>
    Modal.confirm({
      title: t("networks.disconnectConfirm.title", { name: fn.name, network }),
      content: t("networks.disconnectConfirm.content"),
      okText: t("networks.disconnect"),
      okButtonProps: { danger: true },
      cancelText: t("actions.cancel", { ns: "common" }),
      onOk: () =>
        apply(
          fn.networks.filter((n) => n !== network),
          network,
          false,
        ),
    });
  const askEgress = (enabled: boolean) =>
    Modal.confirm({
      title: t(enabled ? "egress.enableTitle" : "egress.disableTitle", {
        name: fn.name,
      }),
      content: (
        <Flex vertical gap="small">
          <Text>{t("egress.recreate")}</Text>
          {enabled && (
            <Alert type="warning" showIcon title={t("egress.warning")} />
          )}
        </Flex>
      ),
      okText: t(enabled ? "egress.enable" : "egress.disable"),
      okButtonProps: { danger: enabled },
      cancelText: t("actions.cancel", { ns: "common" }),
      onOk: async () => {
        try {
          await setEgress({ name: fn.name, enabled });
          toast.success(
            t(enabled ? "egress.enabledToast" : "egress.disabledToast", {
              name: fn.name,
            }),
          );
        } catch (err) {
          toast.error(getApiErrorMessage(err));
        }
      },
    });

  const last = fn.networks.length <= 1;
  const columns: ColumnsType<Row> = [
    {
      title: tn("columns.name"),
      dataIndex: "name",
      render: (name: string) => (
        <Flex gap={4} wrap>
          <Link to={networkPath(name)} style={{ fontWeight: 600 }}>
            {name}
          </Link>
          {name === own && (
            <Tag color="purple">{t("networks.dedicatedTag")}</Tag>
          )}
        </Flex>
      ),
    },
    {
      title: tn("site.columns.type"),
      render: (_: unknown, r) =>
        r.network ? <NetworkScopeTag system={r.network.system} /> : dash,
    },
    {
      title: tn("columns.subnet"),
      render: (_: unknown, r) =>
        r.network?.subnet ? <Text code>{r.network.subnet}</Text> : dash,
    },
    {
      title: tn("members.columns.ipv4"),
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
      title: tn("site.columns.state"),
      render: (_: unknown, r) => {
        if (r.loading) return dash;
        if (!r.network) {
          return <Badge status="error" text={tn("site.unknown")} />;
        }
        return r.member?.present ? (
          <Badge status="success" text={tn("site.connected")} />
        ) : (
          <Tooltip title={t("networks.notConnectedHint")}>
            <Badge status="warning" text={tn("members.notConnected")} />
          </Tooltip>
        );
      },
    },
    {
      title: tn("columns.actions"),
      key: "actions",
      render: (_: unknown, r) => {
        const button = (
          <Button
            size="small"
            danger
            icon={<DisconnectOutlined />}
            disabled={isPending || last}
            aria-label={t("networks.disconnectOf", { network: r.name })}
            onClick={() => askDisconnect(r.name)}
          >
            {t("networks.disconnect")}
          </Button>
        );
        return last ? (
          <Tooltip title={t("networks.lastHint")}>{button}</Tooltip>
        ) : (
          button
        );
      },
    },
  ];

  return (
    <Flex vertical gap="middle">
      <Card
        title={t("networks.title")}
        extra={
          <Button
            icon={<PlusOutlined />}
            disabled={isPending}
            onClick={() => setConnectOpen(true)}
          >
            {t("networks.connect")}
          </Button>
        }
        styles={{ body: { padding: 0 } }}
      >
        <Alert type="info" showIcon banner title={t("networks.intro")} />
        <Table<Row>
          columns={columns}
          dataSource={rows}
          rowKey="name"
          pagination={false}
          scroll={{ x: "max-content" }}
        />
      </Card>
      <Card title={t("egress.label")}>
        <Flex vertical gap="small">
          <Flex gap="small" align="center">
            <Switch
              checked={fn.egress}
              loading={egressPending}
              onChange={askEgress}
              aria-label={t("egress.label")}
            />
            <Text>{fn.egress ? t("egress.on") : t("egress.off")}</Text>
          </Flex>
          <Text type="secondary">{t("egress.help")}</Text>
          {fn.egress && (
            <Alert type="warning" showIcon title={t("egress.warning")} />
          )}
        </Flex>
      </Card>
      <Modal
        open={connectOpen}
        title={t("networks.connectTitle", { name: fn.name })}
        okText={t("networks.connect")}
        okButtonProps={{ disabled: !choice, loading: isPending }}
        cancelText={t("actions.cancel", { ns: "common" })}
        onOk={() => void connect()}
        onCancel={() => {
          setConnectOpen(false);
          setChoice(undefined);
        }}
        destroyOnHidden
      >
        <Flex vertical gap="middle">
          <Text type="secondary">{t("networks.connectIntro")}</Text>
          {available.length === 0 ? (
            <Empty
              image={Empty.PRESENTED_IMAGE_SIMPLE}
              description={t("networks.noneAvailable")}
            />
          ) : (
            <Form layout="vertical" component="div">
              <Form.Item
                label={t("networks.network")}
                htmlFor="fn-connect-network"
                style={{ marginBottom: 0 }}
              >
                <Select
                  id="fn-connect-network"
                  value={choice}
                  onChange={setChoice}
                  placeholder={t("networks.choose")}
                  options={available.map((n) => ({
                    value: n.name,
                    label: `${n.name} (${n.subnet})`,
                  }))}
                />
              </Form.Item>
            </Form>
          )}
          <Text type="secondary">{t("networks.servicesRefused")}</Text>
        </Flex>
      </Modal>
    </Flex>
  );
}
