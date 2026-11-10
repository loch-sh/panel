import {
  ApartmentOutlined,
  CloudDownloadOutlined,
  DeleteOutlined,
  EditOutlined,
  KeyOutlined,
  PlusOutlined,
  SettingOutlined,
  SyncOutlined,
} from "@ant-design/icons";
import {
  Alert,
  Badge,
  Button,
  Card,
  Empty,
  Flex,
  Modal,
  Result,
  Skeleton,
  Space,
  Table,
  Tag,
  Tooltip,
  Typography,
} from "antd";
import { ColumnsType } from "antd/es/table";
import { format, formatDistanceToNow, parseISO } from "date-fns";
import type { TFunction } from "i18next";
import { memo, useCallback, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { useNavigate, useParams } from "react-router";
import { toast } from "react-toastify";
import { PanelTitle } from "../../Components/Base/PanelTitle";
import { useDateFnsLocale } from "../../i18n/useLocales";
import {
  useCheckServiceUpdates,
  useDatabaseDelete,
  useDatabaseRotatePassword,
  useInstanceRetire,
} from "../../Services/Databases/Hooks/useDatabaseMutations";
import useServiceInstances from "../../Services/Databases/Hooks/useServiceInstances";
import useServiceResources from "../../Services/Databases/Hooks/useServiceResources";
import useServicesConfig from "../../Services/Databases/Hooks/useServicesConfig";
import {
  CatalogStatusModel,
  CredentialModel,
  findVersion,
  isServiceEngine,
  ResourceWording,
  resourceWording,
  ServiceEngineModel,
  ServiceInstanceModel,
  ServiceResourceModel,
  updateAvailable,
} from "../../Services/Databases/Models/databases.model";
import useMe from "../../Services/Users/Hooks/useMe";
import { UserProfile } from "../../Services/Users/Models/users.model";
import { getApiErrorMessage } from "../../Utils/apiErrors";
import { NetworkLinks } from "../Networks/NetworkTags";
import CredentialsModal from "./CredentialsModal";
import ExposureModal, { formatHostPort } from "./ExposureModal";
import InstanceNetworksModal from "./InstanceNetworksModal";
import PurgeInstanceModal from "./PurgeInstanceModal";
import DeleteDatabaseModal from "./DeleteDatabaseModal";
import { engineLabel, optionValueLabel } from "./serviceLabels";
import ServiceSupportTags from "./ServiceSupportTags";
import UpdateInstanceModal from "./UpdateInstanceModal";

const { Text } = Typography;

type StateKey =
  | "ready"
  | "starting"
  | "unhealthy"
  | "unverified"
  | "restarting"
  | "paused"
  | "stopped"
  | "missing"
  | "conflict"
  | "other";

/** Maps the Docker state of an instance to a translated label (never shown raw). */
function instanceState(i: ServiceInstanceModel): {
  key: StateKey;
  status: "success" | "processing" | "error" | "default" | "warning";
} {
  if (i.ready) return { key: "ready", status: "success" };
  if (i.state === "running" && i.health === "starting")
    return { key: "starting", status: "processing" };
  if (i.state === "running" && i.health === "unhealthy")
    return { key: "unhealthy", status: "error" };
  if (i.state === "running") return { key: "unverified", status: "warning" };
  if (i.state === "restarting")
    return { key: "restarting", status: "processing" };
  if (i.state === "paused") return { key: "paused", status: "default" };
  if (i.state === "exited" || i.state === "created" || i.state === "dead")
    return { key: "stopped", status: "default" };
  if (i.state === "missing") return { key: "missing", status: "default" };
  if (i.state === "conflict") return { key: "conflict", status: "warning" };
  return { key: "other", status: "warning" };
}

interface DeleteTarget {
  instance: ServiceInstanceModel;
  name: string;
  last: boolean;
}

interface ResourcesTableProps {
  instance: ServiceInstanceModel;
  busy: boolean;
  onRotate: (instance: ServiceInstanceModel, r: ServiceResourceModel) => void;
  onDelete: (
    instance: ServiceInstanceModel,
    r: ServiceResourceModel,
    last: boolean,
  ) => void;
  t: TFunction<"databases">;
  wording: ResourceWording;
}

function getResourceColumns(
  t: TFunction<"databases">,
  busy: boolean,
  lastResource: boolean,
  instance: ServiceInstanceModel,
  onRotate: ResourcesTableProps["onRotate"],
  onDelete: ResourcesTableProps["onDelete"],
  wording: ResourceWording,
): ColumnsType<ServiceResourceModel> {
  return [
    {
      title: t(`wording.${wording}.resourceColumn`),
      dataIndex: "name",
      render: (name: string, r) => (
        <Space>
          <Text strong copyable={{ text: name }}>
            {name}
          </Text>
          {!r.managed && (
            <Tooltip title={t("resources.unmanagedHint")}>
              <Tag>{t("resources.unmanaged")}</Tag>
            </Tooltip>
          )}
        </Space>
      ),
    },
    {
      title: t("resources.columns.actions"),
      width: 120,
      render: (_: unknown, r) =>
        r.managed && (
          <Space.Compact>
            <Tooltip title={t("actions.resetPassword")}>
              <Button
                shape="circle"
                variant="outlined"
                disabled={busy}
                aria-label={t("actions.resetPassword")}
                icon={<KeyOutlined />}
                onClick={() => onRotate(instance, r)}
              />
            </Tooltip>
            <Tooltip title={t("actions.delete", { ns: "common" })}>
              <Button
                danger
                shape="circle"
                disabled={busy}
                aria-label={t("actions.delete", { ns: "common" })}
                icon={<DeleteOutlined />}
                onClick={() => onDelete(instance, r, lastResource)}
              />
            </Tooltip>
          </Space.Compact>
        ),
    },
  ];
}

const ResourcesTable = memo(function ResourcesTable({
  instance,
  busy,
  onRotate,
  onDelete,
  t,
  wording,
}: Readonly<ResourcesTableProps>) {
  const { data, isLoading, isError, error } = useServiceResources(
    instance.key,
    instance.ready,
  );
  const lastResource = (data?.length ?? 0) === 1;
  const columns = useMemo(
    () =>
      getResourceColumns(
        t,
        busy,
        lastResource,
        instance,
        onRotate,
        onDelete,
        wording,
      ),
    [t, busy, lastResource, instance, onRotate, onDelete, wording],
  );
  const locale = useMemo(
    () => ({ emptyText: t(`wording.${wording}.resourcesEmpty`) }),
    [t, wording],
  );
  if (!instance.ready) {
    return (
      <Alert type="warning" showIcon message={t("instances.notReadyHint")} />
    );
  }
  if (isError) {
    return <Alert type="error" showIcon message={getApiErrorMessage(error)} />;
  }
  return (
    <Table<ServiceResourceModel>
      columns={columns}
      dataSource={data}
      loading={isLoading}
      rowKey="name"
      size="small"
      pagination={false}
      locale={locale}
    />
  );
});

function renderState(t: TFunction<"databases">, i: ServiceInstanceModel) {
  const s = instanceState(i);
  return <Badge status={s.status} text={t(`instances.state.${s.key}`)} />;
}

function renderHost(i: ServiceInstanceModel, defaultPort: number | undefined) {
  const host = `${i.project}:${defaultPort ?? ""}`;
  return (
    <Text code copyable={{ text: host }}>
      {host}
    </Text>
  );
}

/** Version of an instance with its support dates and available updates. */
function renderVersion(
  t: TFunction<"databases">,
  engineName: string,
  engine: ServiceEngineModel | undefined,
  i: ServiceInstanceModel,
  strong: boolean,
) {
  const label = `${engineName} ${i.version}`;
  return (
    <Flex vertical gap={4} align="flex-start">
      {strong ? <Text strong>{label}</Text> : <Text>{label}</Text>}
      <ServiceSupportTags
        support={{
          supportUntil: i.supportUntil,
          activeSupportUntil: i.activeSupportUntil,
          deprecated: i.deprecated,
          pinned: findVersion(engine, i.version)?.pinned,
        }}
      />
      {updateAvailable(i) && (
        <Tooltip title={t("update.availableHint")}>
          <Tag color="blue" icon={<CloudDownloadOutlined />}>
            {t("update.available")}
          </Tag>
        </Tooltip>
      )}
    </Flex>
  );
}

/** Callbacks of the access columns (networks and published port). */
interface AccessActions {
  busy: boolean;
  onNetworks: (i: ServiceInstanceModel) => void;
  onExposure: (i: ServiceInstanceModel) => void;
}

function renderExposure(
  t: TFunction<"databases">,
  i: ServiceInstanceModel,
  { busy, onExposure }: AccessActions,
) {
  return (
    <Space size={4} wrap>
      {i.exposure ? (
        <Tooltip
          title={
            i.exposure.tls && i.exposure.fingerprint
              ? t(
                  i.exposure.tlsOptional
                    ? "instances.exposureTlsOptionalHint"
                    : "instances.exposureTlsHint",
                  { fingerprint: i.exposure.fingerprint },
                )
              : t("instances.exposureHint")
          }
        >
          <Tag
            color={
              i.exposure.tls && !i.exposure.tlsOptional ? "green" : "orange"
            }
          >
            {formatHostPort(i.exposure.address, i.exposure.port)}
            {" · "}
            {i.exposure.tls
              ? i.exposure.tlsOptional
                ? t("instances.tlsOptional")
                : t("instances.tls")
              : t("instances.noTls")}
          </Tag>
        </Tooltip>
      ) : (
        <Text type="secondary">{t("instances.notExposed")}</Text>
      )}
      {i.exposureError && (
        <Tooltip title={t("instances.exposureErrorHint")}>
          <Tag color="red">{t("instances.exposureError")}</Tag>
        </Tooltip>
      )}
      {!i.modified && (
        <Tooltip title={t("exposure.edit")}>
          <Button
            size="small"
            type="text"
            icon={<SettingOutlined />}
            aria-label={t("exposure.edit")}
            disabled={busy}
            onClick={() => onExposure(i)}
          />
        </Tooltip>
      )}
    </Space>
  );
}

function renderNetworks(
  t: TFunction<"databases">,
  i: ServiceInstanceModel,
  { busy, onNetworks }: AccessActions,
) {
  return (
    <Space size={4} wrap>
      <NetworkLinks networks={i.networks} />
      {!i.modified && (
        <Tooltip title={t("networks.edit")}>
          <Button
            size="small"
            type="text"
            icon={<ApartmentOutlined />}
            aria-label={t("networks.edit")}
            disabled={busy}
            onClick={() => onNetworks(i)}
          />
        </Tooltip>
      )}
    </Space>
  );
}

function accessColumns(
  t: TFunction<"databases">,
  actions: AccessActions,
): ColumnsType<ServiceInstanceModel> {
  return [
    {
      title: t("networks.column"),
      render: (_: unknown, i) => renderNetworks(t, i, actions),
    },
    {
      title: t("instances.columns.exposure"),
      responsive: ["md"],
      render: (_: unknown, i) => renderExposure(t, i, actions),
    },
  ];
}

/** Dedicated engines: one row per instance, which is the resource. */
function getDedicatedColumns(
  t: TFunction<"databases">,
  lang: string | undefined,
  engineName: string,
  engine: ServiceEngineModel | undefined,
  busy: boolean,
  /** null: the engine has no password (ElasticMQ) */
  onRotate: ((i: ServiceInstanceModel) => void) | null,
  onDelete: (i: ServiceInstanceModel) => void,
  onEdit: (i: ServiceInstanceModel) => void,
  onPurge: (i: ServiceInstanceModel) => void,
  onUpgrade: (i: ServiceInstanceModel) => void,
  access: AccessActions,
): ColumnsType<ServiceInstanceModel> {
  const defaultPort = engine?.defaultPort;
  return [
    {
      title: t("dedicated.columns.name"),
      dataIndex: "name",
      render: (name: string, i) => (
        <Space>
          <Text strong copyable={{ text: name }}>
            {name}
          </Text>
          {i.modified && (
            <Tooltip title={t("instances.modifiedHint")}>
              <Tag color="purple">{t("instances.modified")}</Tag>
            </Tooltip>
          )}
        </Space>
      ),
    },
    {
      title: t("dedicated.columns.version"),
      dataIndex: "version",
      render: (_: string, i) => renderVersion(t, engineName, engine, i, false),
    },
    {
      title: t("dedicated.columns.size"),
      render: (_: unknown, i) =>
        i.options?.memory ? (
          <Space size={4} wrap>
            <Tag>{optionValueLabel(t, "memory", i.options.memory, lang)}</Tag>
            {i.options.storage && (
              <Tag>
                {optionValueLabel(t, "storage", i.options.storage, lang)}
              </Tag>
            )}
            {i.options.quota && (
              <Tag>{optionValueLabel(t, "quota", i.options.quota, lang)}</Tag>
            )}
          </Space>
        ) : (
          <Text type="secondary">—</Text>
        ),
    },
    {
      title: t("instances.columns.state"),
      render: (_: unknown, i) => renderState(t, i),
    },
    {
      title: t("instances.columns.host"),
      responsive: ["md"],
      render: (_: unknown, i) => renderHost(i, defaultPort),
    },
    ...accessColumns(t, access),
    {
      title: t("instances.columns.actions"),
      width: 190,
      render: (_: unknown, i) =>
        i.modified ? (
          <PurgeButton t={t} disabled={busy} onClick={() => onPurge(i)} />
        ) : (
          <Space.Compact>
            {updateAvailable(i) && (
              <Tooltip title={t("update.action")}>
                <Button
                  shape="circle"
                  variant="outlined"
                  color="primary"
                  disabled={busy || !i.ready}
                  aria-label={t("update.action")}
                  icon={<CloudDownloadOutlined />}
                  onClick={() => onUpgrade(i)}
                />
              </Tooltip>
            )}
            <Tooltip title={t("actions.edit")}>
              <Button
                shape="circle"
                variant="outlined"
                disabled={busy || !i.ready}
                aria-label={t("actions.edit")}
                icon={<EditOutlined />}
                onClick={() => onEdit(i)}
              />
            </Tooltip>
            {onRotate && (
              <Tooltip title={t("actions.resetPassword")}>
                <Button
                  shape="circle"
                  variant="outlined"
                  disabled={busy || !i.ready}
                  aria-label={t("actions.resetPassword")}
                  icon={<KeyOutlined />}
                  onClick={() => onRotate(i)}
                />
              </Tooltip>
            )}
            <Tooltip title={t("actions.delete", { ns: "common" })}>
              <Button
                danger
                shape="circle"
                disabled={busy}
                aria-label={t("actions.delete", { ns: "common" })}
                icon={<DeleteOutlined />}
                onClick={() => onDelete(i)}
              />
            </Tooltip>
          </Space.Compact>
        ),
    },
  ];
}

function getInstanceColumns(
  t: TFunction<"databases">,
  engineName: string,
  engine: ServiceEngineModel | undefined,
  busy: boolean,
  onRetire: (instance: ServiceInstanceModel) => void,
  onPurge: (instance: ServiceInstanceModel) => void,
  wording: ResourceWording,
  access: AccessActions,
): ColumnsType<ServiceInstanceModel> {
  const defaultPort = engine?.defaultPort;
  return [
    {
      title: t("instances.columns.version"),
      dataIndex: "version",
      render: (_: string, i) => (
        <Space align="start">
          {renderVersion(t, engineName, engine, i, true)}
          {i.modified && (
            <Tooltip title={t("instances.modifiedHint")}>
              <Tag color="purple">{t("instances.modified")}</Tag>
            </Tooltip>
          )}
        </Space>
      ),
    },
    {
      title: t("instances.columns.state"),
      render: (_: unknown, i) => renderState(t, i),
    },
    {
      title: t(`wording.${wording}.countColumn`),
      dataIndex: "resources",
      render: (n: number | null) =>
        n === null ? <Text type="secondary">—</Text> : n,
    },
    {
      title: t("instances.columns.host"),
      responsive: ["md"],
      render: (_: unknown, i) => renderHost(i, defaultPort),
    },
    ...accessColumns(t, access),
    {
      title: t("instances.columns.actions"),
      width: 160,
      render: (_: unknown, i) =>
        i.modified ? (
          <PurgeButton t={t} disabled={busy} onClick={() => onPurge(i)} />
        ) : (
          i.ready &&
          i.resources === 0 && (
            <Button
              danger
              size="small"
              disabled={busy}
              onClick={() => onRetire(i)}
            >
              {t("actions.retire")}
            </Button>
          )
        ),
    },
  ];
}

/** Deletes an instance edited by hand (confirmed in PurgeInstanceModal). */
function PurgeButton({
  t,
  disabled,
  onClick,
}: Readonly<{
  t: TFunction<"databases">;
  disabled: boolean;
  onClick: () => void;
}>) {
  return (
    <Button danger size="small" disabled={disabled} onClick={onClick}>
      {t("actions.purge")}
    </Button>
  );
}

/** Engine names of a list of engine ids ("Redis®, NATS"). */
function engineList(t: TFunction<"databases">, ids: string[]): string {
  return ids.map((id) => engineLabel(t, id)).join(", ");
}

/**
 * Shown instead of the empty table while an engine has nothing created yet:
 * what the service does and a button to create the first resource.
 */
function EnginePresentation({
  engineId,
  engineName,
  wording,
  disabled,
  onCreate,
}: Readonly<{
  engineId: string;
  engineName: string;
  wording: ResourceWording;
  disabled: boolean;
  onCreate: () => void;
}>) {
  const { t } = useTranslation("databases");
  return (
    <Card>
      <Empty
        image={Empty.PRESENTED_IMAGE_SIMPLE}
        description={
          <Flex vertical align="center" gap="small">
            <Typography.Title level={5} style={{ margin: 0 }}>
              {engineName}
            </Typography.Title>
            <Typography.Paragraph
              type="secondary"
              style={{ maxWidth: 640, marginBottom: 0 }}
            >
              {isServiceEngine(engineId)
                ? t(`presentation.${engineId}`)
                : t(`wording.${wording}.empty`, { engine: engineName })}
            </Typography.Paragraph>
          </Flex>
        }
      >
        <Button
          type="primary"
          icon={<PlusOutlined />}
          disabled={disabled}
          onClick={onCreate}
        >
          {t(`wording.${wording}.createFirst`)}
        </Button>
      </Empty>
    </Card>
  );
}

/** When the versions were last discovered (registries). */
function CatalogChecked({
  catalog,
}: Readonly<{ catalog: CatalogStatusModel | undefined }>) {
  const { t } = useTranslation("databases");
  const locale = useDateFnsLocale();
  if (!catalog) return null;
  const at = catalog.checkedAt ? parseISO(catalog.checkedAt) : null;
  if (!at || Number.isNaN(at.getTime())) {
    return <Text type="secondary">{t("catalog.never")}</Text>;
  }
  return (
    <Tooltip title={format(at, "Pp", { locale })}>
      <Text type="secondary">
        {t("catalog.checked", {
          when: formatDistanceToNow(at, { addSuffix: true, locale }),
        })}
      </Text>
    </Tooltip>
  );
}

export default function Databases() {
  const { t, i18n } = useTranslation("databases");
  const { engine: engineParam = "" } = useParams();
  const navigate = useNavigate();
  const { data: me, isLoading: meLoading } = useMe();
  const isAdmin = me?.profile === UserProfile.ADMIN;
  const {
    data: config,
    isLoading: loadingConfig,
    error: configError,
  } = useServicesConfig(isAdmin);
  const enabled = config?.enabled ?? false;
  const {
    data: allInstances,
    isLoading: loadingInstances,
    isSuccess: instancesLoaded,
  } = useServiceInstances(enabled);

  const engine = useMemo(
    () => config?.engines?.find((e) => e.id === engineParam),
    [config, engineParam],
  );
  const engineName = engineLabel(t, engineParam);
  const wording = resourceWording(engine, engineParam);
  const dedicated = engine?.lifecycle === "dedicated";
  const instances = useMemo(
    () => (allInstances ?? []).filter((i) => i.engine === engineParam),
    [allInstances, engineParam],
  );

  const { mutateAsync: deleteDatabase, isPending: deleting } =
    useDatabaseDelete();
  const { mutateAsync: rotatePassword, isPending: rotating } =
    useDatabaseRotatePassword();
  const { mutateAsync: retireInstance, isPending: retiring } =
    useInstanceRetire();
  const busy = deleting || rotating || retiring;

  const [credentials, setCredentials] = useState<CredentialModel[] | null>(
    null,
  );
  const [deleteTarget, setDeleteTarget] = useState<DeleteTarget | null>(null);
  const [purgeTarget, setPurgeTarget] = useState<ServiceInstanceModel | null>(
    null,
  );
  const [networksTarget, setNetworksTarget] =
    useState<ServiceInstanceModel | null>(null);
  const [exposureTarget, setExposureTarget] =
    useState<ServiceInstanceModel | null>(null);
  const [upgradeTarget, setUpgradeTarget] =
    useState<ServiceInstanceModel | null>(null);
  const { mutateAsync: checkUpdates, isPending: checking } =
    useCheckServiceUpdates();
  const handleCheckUpdates = useCallback(async () => {
    try {
      const status = await checkUpdates();
      if (status.throttled) {
        toast.info(t("catalog.throttled"));
      } else if (status.failed?.length) {
        toast.warning(
          t("catalog.failed", { engines: engineList(t, status.failed) }),
        );
      } else {
        toast.success(t("catalog.done"));
      }
    } catch (err) {
      toast.error(getApiErrorMessage(err));
    }
  }, [checkUpdates, t]);
  const access = useMemo<AccessActions>(
    () => ({
      busy,
      onNetworks: setNetworksTarget,
      onExposure: setExposureTarget,
    }),
    [busy],
  );

  const rotate = useCallback(
    (instanceKey: string, name: string) => {
      Modal.confirm({
        title: t("rotate.title", { name }),
        content: t("rotate.content"),
        okText: t("rotate.submit"),
        cancelText: t("actions.cancel", { ns: "common" }),
        onOk: async () => {
          try {
            setCredentials(
              await rotatePassword({ instance: instanceKey, name }),
            );
            toast.success(t("toasts.passwordReset"));
          } catch (err) {
            toast.error(getApiErrorMessage(err));
          }
        },
      });
    },
    [rotatePassword, t],
  );
  const handleRotate = useCallback(
    (instance: ServiceInstanceModel, r: ServiceResourceModel) =>
      rotate(instance.key, r.name),
    [rotate],
  );

  const handleDelete = useCallback(async () => {
    if (!deleteTarget) return;
    try {
      const res = await deleteDatabase({
        instance: deleteTarget.instance.key,
        name: deleteTarget.name,
      });
      if (dedicated) toast.success(t(`wording.${wording}.deleted`));
      else if (res.instanceRetired)
        toast.success(t(`wording.${wording}.instanceRetired`));
      else if (res.retireBlocked)
        toast.warning(t(`wording.${wording}.retireBlocked`));
      else toast.success(t(`wording.${wording}.deleted`));
      setDeleteTarget(null);
    } catch (err) {
      toast.error(getApiErrorMessage(err));
    }
  }, [deleteDatabase, deleteTarget, t, dedicated, wording]);

  const handleAskDelete = useCallback(
    (i: ServiceInstanceModel, r: ServiceResourceModel, last: boolean) =>
      setDeleteTarget({ instance: i, name: r.name, last }),
    [],
  );

  const handleRetire = useCallback(
    (instance: ServiceInstanceModel) => {
      Modal.confirm({
        title: t("retire.title", {
          engine: engineName,
          version: instance.version,
        }),
        content: t("retire.content"),
        okText: t("retire.submit"),
        okButtonProps: { danger: true },
        cancelText: t("actions.cancel", { ns: "common" }),
        onOk: async () => {
          try {
            await retireInstance(instance.key);
            toast.success(t("toasts.retired"));
          } catch (err) {
            toast.error(getApiErrorMessage(err));
          }
        },
      });
    },
    [engineName, retireInstance, t],
  );

  const rotateDedicated = useCallback(
    (i: ServiceInstanceModel) => i.name && rotate(i.key, i.name),
    [rotate],
  );
  const editDedicated = useCallback(
    (i: ServiceInstanceModel) =>
      navigate(
        `/services/${encodeURIComponent(engineParam)}/${encodeURIComponent(i.name ?? "")}/edit`,
      ),
    [navigate, engineParam],
  );
  const deleteDedicated = useCallback(
    (i: ServiceInstanceModel) =>
      i.name && setDeleteTarget({ instance: i, name: i.name, last: false }),
    [],
  );
  const columns = useMemo(
    () =>
      dedicated
        ? getDedicatedColumns(
            t,
            i18n.resolvedLanguage,
            engineName,
            engine,
            busy,
            engine?.unauthenticated ? null : rotateDedicated,
            deleteDedicated,
            editDedicated,
            setPurgeTarget,
            setUpgradeTarget,
            access,
          )
        : getInstanceColumns(
            t,
            engineName,
            engine,
            busy,
            handleRetire,
            setPurgeTarget,
            wording,
            access,
          ),
    [
      t,
      i18n.resolvedLanguage,
      dedicated,
      engineName,
      engine,
      busy,
      handleRetire,
      rotateDedicated,
      deleteDedicated,
      editDedicated,
      wording,
      access,
    ],
  );
  const goToCreate = useCallback(
    () => navigate(`/services/${encodeURIComponent(engineParam)}/new`),
    [navigate, engineParam],
  );
  // Only once the instances are loaded (not while loading, not on error).
  const showPresentation =
    !!engine && instancesLoaded && instances.length === 0;
  const instancesLocale = useMemo(
    () => ({
      emptyText: (
        <Empty
          description={t(`wording.${wording}.empty`, { engine: engineName })}
        />
      ),
    }),
    [t, engineName, wording],
  );
  // Every instance is expanded, also those loaded after the first render,
  // unless the user collapsed it.
  const [collapsed, setCollapsed] = useState<Set<string>>(new Set());
  const expandedRowKeys = useMemo(
    () => instances.map((i) => i.key).filter((k) => !collapsed.has(k)),
    [instances, collapsed],
  );
  const expandable = useMemo(
    () => ({
      expandedRowKeys,
      onExpand: (expanded: boolean, i: ServiceInstanceModel) =>
        setCollapsed((prev) => {
          const next = new Set(prev);
          if (expanded) next.delete(i.key);
          else next.add(i.key);
          return next;
        }),
      expandedRowRender: (instance: ServiceInstanceModel) => (
        <ResourcesTable
          instance={instance}
          busy={busy}
          t={t}
          wording={wording}
          onRotate={handleRotate}
          onDelete={handleAskDelete}
        />
      ),
    }),
    [busy, t, handleRotate, handleAskDelete, wording, expandedRowKeys],
  );

  if (meLoading) return null;
  if (!isAdmin) return <Result status="403" title={t("forbidden")} />;
  if (configError) {
    return (
      <Alert type="error" showIcon message={getApiErrorMessage(configError)} />
    );
  }
  // The wording (title, intro, buttons) depends on the engine's lifecycle,
  // known only once the configuration is loaded: no "databases" title on a
  // dedicated engine in the meantime.
  if (loadingConfig) return <Skeleton active />;
  if (!loadingConfig && config && !config.enabled) {
    return (
      <Flex vertical gap="middle">
        <PanelTitle>
          {t(`wording.${wording}.title`, { engine: engineName })}
        </PanelTitle>
        <Alert
          type={config.error ? "error" : "info"}
          showIcon
          message={config.error ? t("configError") : t("disabled")}
        />
      </Flex>
    );
  }
  if (!loadingConfig && config?.enabled && !engine) {
    return (
      <Flex vertical gap="middle">
        <PanelTitle>
          {t(`wording.${wording}.title`, { engine: engineName })}
        </PanelTitle>
        <Alert type="info" showIcon message={t("engineDisabled")} />
      </Flex>
    );
  }

  return (
    <>
      <Flex gap="middle" vertical>
        <Flex justify="space-between" align="center" wrap gap="small">
          <PanelTitle>
            {t(`wording.${wording}.title`, { engine: engineName })}
          </PanelTitle>
          <Flex gap="small" align="center" wrap>
            <CatalogChecked catalog={config?.catalog} />
            <Button
              icon={<SyncOutlined spin={checking} />}
              disabled={!config?.enabled || checking}
              onClick={() => void handleCheckUpdates()}
            >
              {t("catalog.check")}
            </Button>
            <Button
              type="primary"
              icon={<PlusOutlined />}
              disabled={!engine || busy}
              onClick={goToCreate}
            >
              {t(`wording.${wording}.newResource`)}
            </Button>
          </Flex>
        </Flex>
        <Typography.Paragraph type="secondary" style={{ marginBottom: 0 }}>
          {t(`wording.${wording}.intro`)}
        </Typography.Paragraph>

        {config?.catalog?.failed?.length ? (
          <Alert
            type="warning"
            showIcon
            message={t("catalog.failed", {
              engines: engineList(t, config.catalog.failed),
            })}
          />
        ) : null}
        {engine?.unauthenticated && (
          <Alert
            type="warning"
            showIcon
            message={t("createPage.unauthenticated")}
          />
        )}
        {showPresentation ? (
          <EnginePresentation
            engineId={engineParam}
            engineName={engineName}
            wording={wording}
            disabled={!engine || busy}
            onCreate={goToCreate}
          />
        ) : (
          <Table<ServiceInstanceModel>
            columns={columns}
            dataSource={instances}
            loading={loadingConfig || loadingInstances}
            rowKey="key"
            pagination={false}
            scroll={{ x: "max-content" }}
            locale={instancesLocale}
            expandable={dedicated ? undefined : expandable}
          />
        )}
      </Flex>

      <CredentialsModal
        credentials={credentials}
        onClose={() => setCredentials(null)}
      />
      <DeleteDatabaseModal
        target={
          deleteTarget && {
            name: deleteTarget.name,
            last: deleteTarget.last,
            engineName,
            version: deleteTarget.instance.version,
            wording,
          }
        }
        loading={deleting}
        onCancel={() => setDeleteTarget(null)}
        onConfirm={handleDelete}
      />
      <PurgeInstanceModal
        instance={purgeTarget}
        onClose={() => setPurgeTarget(null)}
      />
      <InstanceNetworksModal
        instance={networksTarget}
        onClose={() => setNetworksTarget(null)}
      />
      <UpdateInstanceModal
        instance={upgradeTarget}
        engine={engine}
        engineName={engineName}
        onClose={() => setUpgradeTarget(null)}
      />
      <ExposureModal
        instance={exposureTarget}
        engine={engine}
        limits={config?.exposure}
        onClose={() => setExposureTarget(null)}
      />
    </>
  );
}
