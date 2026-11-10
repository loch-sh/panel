import {
  ArrowLeftOutlined,
  DeleteOutlined,
  DisconnectOutlined,
  PlusOutlined,
} from "@ant-design/icons";
import { useQueryClient } from "@tanstack/react-query";
import {
  Alert,
  Button,
  Card,
  Descriptions,
  Flex,
  List,
  Modal,
  Result,
  Skeleton,
  Space,
  Tooltip,
  Typography,
} from "antd";
import { isAxiosError } from "axios";
import { useCallback, useEffect, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { useLocation, useNavigate, useParams } from "react-router";
import { toast } from "react-toastify";
import { PanelTitle } from "../../Components/Base/PanelTitle";
import useServiceInstances from "../../Services/Databases/Hooks/useServiceInstances";
import useEnabledFunctions from "../../Services/Functions/Hooks/useEnabledFunctions";
import { FunctionSummaryModel } from "../../Services/Functions/Models/functions.model";
import useServicesConfig from "../../Services/Databases/Hooks/useServicesConfig";
import { ServiceInstanceModel } from "../../Services/Databases/Models/databases.model";
import useHostingConfig from "../../Services/Hosting/Hooks/useHostingConfig";
import useHostingStacks from "../../Services/Hosting/Hooks/useHostingStacks";
import { invalidateAfterOperation } from "../../Services/Hosting/invalidateAfterOperation";
import useNetwork from "../../Services/Networks/Hooks/useNetwork";
import {
  useNetworkAttach,
  useNetworkDetach,
  useNetworkDisconnect,
  useNetworkUpdate,
} from "../../Services/Networks/Hooks/useNetworkMutations";
import {
  AttachableKind,
  NETWORK_DESCRIPTION_MAX_LENGTH,
  NetworkMemberModel,
} from "../../Services/Networks/Models/networks.model";
import { networksServiceKeys } from "../../Services/Networks/networks.service";
import useMe from "../../Services/Users/Hooks/useMe";
import { UserProfile } from "../../Services/Users/Models/users.model";
import { getApiErrorMessage } from "../../Utils/apiErrors";
import AttachMemberModal, { AttachTarget } from "./AttachMemberModal";
import MembersTable from "./MembersTable";
import { CreatedNetworkState, NETWORKS_PATH } from "./networkPaths";
import {
  NetworkScopeTag,
  NetworkStateBadge,
  NoInternetTag,
  ReservedOverlapTag,
} from "./NetworkTags";
import useDeleteNetwork from "./useDeleteNetwork";

const { Text, Paragraph } = Typography;

/** A date and time in the UI language ("" when invalid). */
function formatDateTime(value: string, lang: string | undefined): string {
  const d = new Date(value);
  if (Number.isNaN(d.getTime())) return "";
  return new Intl.DateTimeFormat(lang, {
    dateStyle: "medium",
    timeStyle: "short",
  }).format(d);
}

/** Sites redeployed after an attach / detach: stack name -> operation id. */
type PendingSites = Record<string, string>;

/**
 * Tracks the redeployment of sites attached to / detached from the network,
 * like the hosting pages: toast when their operation ends.
 */
function useSiteOperations(enabled: boolean, initial: PendingSites = {}) {
  const { t } = useTranslation("networks");
  const client = useQueryClient();
  const [pending, setPending] = useState<PendingSites>(initial);
  const watching = Object.keys(pending).length > 0;
  const { data: stacks } = useHostingStacks(enabled);

  useEffect(() => {
    if (!stacks || !watching) return;
    const done: string[] = [];
    for (const [name, opId] of Object.entries(pending)) {
      const stack = stacks.find((s) => s.name === name);
      const op = stack?.operation;
      // The stack is gone, or another operation started after ours: stop
      // watching it (no outcome to report). A finished older operation
      // still shown before the refetch is not ours: keep waiting.
      if (!stack || (op && op.id !== opId && op.state === "running")) {
        done.push(name);
        continue;
      }
      if (!op || op.state === "running") continue;
      done.push(name);
      if (op.state === "succeeded") {
        toast.success(t("toasts.siteRedeployed", { name }));
      } else {
        toast.error(t("toasts.siteRedeployFailed", { name }));
      }
    }
    if (done.length === 0) return;
    invalidateAfterOperation(client);
    client.invalidateQueries({ queryKey: [networksServiceKeys.network] });
    client.invalidateQueries({ queryKey: [networksServiceKeys.networks] });
    // Derived from the polled stacks (external data), not from local state.
    setPending((prev) => {
      const next = { ...prev };
      for (const n of done) delete next[n];
      return next;
    });
  }, [stacks, pending, watching, client, t]);

  const track = useCallback((name: string, operation?: string) => {
    if (operation) setPending((prev) => ({ ...prev, [name]: operation }));
  }, []);
  return { stacks: stacks ?? [], track, watching };
}

/** A private network: information, members, attach and detach. */
export default function NetworkDetail() {
  const { t, i18n } = useTranslation("networks");
  const { name = "" } = useParams();
  const navigate = useNavigate();
  // Set by the creation page: sites being redeployed, members that failed.
  const location = useLocation();
  const fromCreate = (location.state ?? null) as CreatedNetworkState | null;
  const [failedMembers, setFailedMembers] = useState(
    fromCreate?.failedMembers ?? [],
  );
  // Read once: a reload must not report these again.
  useEffect(() => {
    if (location.state) {
      navigate(location.pathname, { replace: true, state: null });
    }
  }, [location.pathname, location.state, navigate]);
  const { data: me, isLoading: meLoading } = useMe();
  const isAdmin = me?.profile === UserProfile.ADMIN;
  const { data: hostingConfig } = useHostingConfig(isAdmin);
  const hostingEnabled = hostingConfig?.enabled ?? false;
  const { data: servicesConfig } = useServicesConfig(isAdmin);
  const { data: instances } = useServiceInstances(
    servicesConfig?.enabled ?? false,
  );
  const { stacks, track, watching } = useSiteOperations(
    isAdmin && hostingEnabled,
    fromCreate?.pendingSites,
  );
  const {
    data: network,
    isLoading,
    error,
  } = useNetwork(name, { enabled: isAdmin, fastPoll: watching });

  const { mutateAsync: attach, isPending: attaching } = useNetworkAttach();
  const { mutateAsync: detach, isPending: detaching } = useNetworkDetach();
  const { mutateAsync: disconnect, isPending: disconnecting } =
    useNetworkDisconnect();
  const { mutateAsync: update } = useNetworkUpdate();
  const { ask: askDelete, isPending: deleting } = useDeleteNetwork(() =>
    navigate(NETWORKS_PATH),
  );
  const busy = attaching || detaching || disconnecting || deleting;
  const [attachOpen, setAttachOpen] = useState(false);

  const functions = useEnabledFunctions(isAdmin);
  const functionsByName = useMemo(
    () =>
      new Map<string, FunctionSummaryModel>(
        (functions ?? []).map((f) => [f.name, f]),
      ),
    [functions],
  );
  const instancesByKey = useMemo(
    () =>
      new Map<string, ServiceInstanceModel>(
        (instances ?? []).map((i) => [i.key, i]),
      ),
    [instances],
  );
  const managed = useMemo(
    () => (network?.members ?? []).filter((m) => m.kind !== "unmanaged"),
    [network],
  );
  const unmanaged = useMemo(
    () => (network?.members ?? []).filter((m) => m.kind === "unmanaged"),
    [network],
  );

  const afterMemberChange = useCallback(
    (kind: AttachableKind, member: string, attached: boolean, op?: string) => {
      if (kind === "site" && op) {
        track(member, op);
        toast.info(t("toasts.siteRedeploying", { name: member }));
      } else {
        toast.success(
          t(attached ? "toasts.attached" : "toasts.detached", {
            name: member,
          }),
        );
      }
    },
    [t, track],
  );

  const onAttach = async (target: AttachTarget) => {
    try {
      const res = await attach({ network: name, ...target });
      setAttachOpen(false);
      afterMemberChange(target.kind, target.name, true, res.operation);
    } catch (err) {
      toast.error(getApiErrorMessage(err));
    }
  };

  const onDetach = useCallback(
    (m: NetworkMemberModel) => {
      const kind = m.kind as AttachableKind;
      const container =
        instancesByKey.get(m.name)?.project ?? m.container ?? m.name;
      Modal.confirm({
        title: t("members.detachConfirm.title", {
          name: m.name,
          network: name,
        }),
        content:
          kind === "site"
            ? t("members.detachConfirm.site")
            : kind === "function"
              ? t("members.detachConfirm.function", { name: m.name })
              : t("members.detachConfirm.instance", { container }),
        okText: t("members.detachConfirm.submit"),
        okButtonProps: { danger: true },
        cancelText: t("actions.cancel", { ns: "common" }),
        onOk: async () => {
          try {
            const res = await detach({ network: name, kind, name: m.name });
            afterMemberChange(kind, m.name, false, res.operation);
          } catch (err) {
            toast.error(getApiErrorMessage(err));
          }
        },
      });
    },
    [afterMemberChange, detach, instancesByKey, name, t],
  );

  const onDisconnect = (m: NetworkMemberModel) => {
    if (!m.containerId) return;
    const containerId = m.containerId;
    const label = m.container ?? m.name;
    Modal.confirm({
      title: t("unmanaged.confirm.title", { name: label }),
      content: t("unmanaged.confirm.content"),
      okText: t("unmanaged.confirm.submit"),
      okButtonProps: { danger: true },
      cancelText: t("actions.cancel", { ns: "common" }),
      onOk: async () => {
        try {
          await disconnect({ network: name, containerId });
          toast.success(t("toasts.disconnected", { name: label }));
        } catch (err) {
          toast.error(getApiErrorMessage(err));
        }
      },
    });
  };

  const saveDescription = async (value: string) => {
    if (!network || value === (network.description ?? "")) return;
    try {
      await update({ name, description: value });
      toast.success(t("toasts.descriptionSaved"));
    } catch (err) {
      toast.error(getApiErrorMessage(err));
    }
  };

  if (meLoading) return null;
  if (!isAdmin) return <Result status="403" title={t("forbidden")} />;

  const header = (
    <Flex gap="small" align="center" wrap>
      <Button
        type="text"
        icon={<ArrowLeftOutlined />}
        aria-label={t("detail.back")}
        onClick={() => navigate(NETWORKS_PATH)}
      />
      <PanelTitle>{name}</PanelTitle>
      {network && (
        <Space size={4} wrap>
          <NetworkScopeTag system={network.system} />
          {network.internal && <NoInternetTag />}
          <NetworkStateBadge state={network.state} problem={network.problem} />
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
          message={notFound ? t("detail.notFound") : getApiErrorMessage(error)}
        />
      </Flex>
    );
  }

  const created = formatDateTime(network.createdAt, i18n.resolvedLanguage);

  return (
    <Flex vertical gap="middle" style={{ maxWidth: 1400 }}>
      <Flex justify="space-between" align="center" wrap gap="small">
        {header}
        <Space wrap>
          <Button
            type="primary"
            icon={<PlusOutlined />}
            disabled={busy}
            onClick={() => setAttachOpen(true)}
          >
            {t("detail.attach")}
          </Button>
          <Tooltip
            title={
              network.system
                ? t("delete.systemHint")
                : managed.length + unmanaged.length > 0
                  ? t("delete.inUseHint")
                  : undefined
            }
          >
            <Button
              danger
              icon={<DeleteOutlined />}
              disabled={busy || network.system}
              onClick={() => askDelete(name)}
            >
              {t("actions.delete", { ns: "common" })}
            </Button>
          </Tooltip>
        </Space>
      </Flex>

      {network.system && (
        <Alert type="info" showIcon message={t("detail.systemNote")} />
      )}
      {network.state !== "ok" && (
        <Alert
          type={network.state === "compromised" ? "error" : "warning"}
          showIcon
          message={t(`state.${network.state}`)}
          description={t(`stateHints.${network.state}`)}
        />
      )}

      {failedMembers.length > 0 && (
        <Alert
          type="warning"
          showIcon
          closable
          onClose={() => setFailedMembers([])}
          message={t("create.members.failedTitle", {
            count: failedMembers.length,
          })}
          description={
            <Flex vertical gap={4}>
              {failedMembers.map((f) => (
                <Text key={`${f.kind}/${f.name}`}>
                  <Text strong>{f.name}</Text>
                  {": "}
                  {f.message}
                </Text>
              ))}
              <Text type="secondary">{t("create.members.failedHint")}</Text>
            </Flex>
          }
        />
      )}
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
              key: "docker",
              label: t("detail.dockerName"),
              children: (
                <Text code copyable={{ text: network.dockerName }}>
                  {network.dockerName}
                </Text>
              ),
            },
            {
              key: "id",
              label: t("detail.id"),
              children: network.id ? (
                <Tooltip title={network.id}>
                  <Text code>{network.id.slice(0, 12)}</Text>
                </Tooltip>
              ) : (
                <Text type="secondary">—</Text>
              ),
            },
            {
              key: "subnet",
              label: t("detail.subnet"),
              children: network.subnet ? (
                <Text code>{network.subnet}</Text>
              ) : (
                <Text type="secondary">—</Text>
              ),
            },
            {
              key: "gateway",
              label: t("detail.gateway"),
              children: network.gateway ? (
                <Text code>{network.gateway}</Text>
              ) : (
                <Text type="secondary">—</Text>
              ),
            },
            {
              key: "internet",
              label: t("detail.internet"),
              children: network.internal ? (
                t("detail.internetNone")
              ) : (
                <Text type="danger">{t("detail.internetWarning")}</Text>
              ),
            },
            {
              key: "created",
              label: t("detail.created"),
              children: network.createdBy
                ? t("detail.createdBy", {
                    date: created,
                    user: network.createdBy,
                  })
                : created || "—",
            },
            {
              key: "description",
              label: t("detail.description"),
              span: "filled",
              children: (
                <Paragraph
                  style={{ marginBottom: 0 }}
                  type={network.description ? undefined : "secondary"}
                  editable={{
                    text: network.description ?? "",
                    maxLength: NETWORK_DESCRIPTION_MAX_LENGTH,
                    tooltip: t("detail.editDescription"),
                    triggerType: ["icon"],
                    onChange: (v) => void saveDescription(v.trim()),
                  }}
                >
                  {network.description || t("detail.descriptionEmpty")}
                </Paragraph>
              ),
            },
          ]}
        />
      </Card>

      {unmanaged.length > 0 && (
        <Alert
          type="warning"
          showIcon
          message={t("unmanaged.title", { count: unmanaged.length })}
          description={
            <Flex vertical gap="small">
              <Text>{t("unmanaged.description")}</Text>
              <List
                size="small"
                dataSource={unmanaged}
                rowKey={(m) => m.containerId ?? m.name}
                renderItem={(m) => (
                  <List.Item
                    actions={[
                      <Button
                        key="disconnect"
                        size="small"
                        danger
                        icon={<DisconnectOutlined />}
                        disabled={busy || !m.containerId}
                        onClick={() => onDisconnect(m)}
                      >
                        {t("unmanaged.disconnect")}
                      </Button>,
                    ]}
                  >
                    <Space size="small" wrap>
                      <Text code>{m.container ?? m.name}</Text>
                      {m.ipv4 && <Text type="secondary">{m.ipv4}</Text>}
                    </Space>
                  </List.Item>
                )}
              />
            </Flex>
          }
        />
      )}

      <Card title={t("detail.members")} styles={{ body: { padding: 0 } }}>
        <MembersTable
          members={managed}
          instances={instancesByKey}
          functions={functionsByName}
          busy={busy}
          onDetach={onDetach}
        />
      </Card>

      <AttachMemberModal
        open={attachOpen}
        network={network}
        stacks={hostingEnabled ? stacks : []}
        instances={instances ?? []}
        functions={functions}
        loading={attaching}
        onCancel={() => setAttachOpen(false)}
        onSubmit={(target) => void onAttach(target)}
      />
    </Flex>
  );
}
