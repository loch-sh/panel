import { ArrowLeftOutlined } from "@ant-design/icons";
import {
  Alert,
  Button,
  Card,
  Col,
  Descriptions,
  Flex,
  Form,
  Grid,
  Input,
  InputNumber,
  Modal,
  Result,
  Row,
  Skeleton,
  Switch,
  Tag,
  Typography,
  theme,
} from "antd";
import { useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { useNavigate, useParams } from "react-router";
import { toast } from "react-toastify";
import {
  BottomActionBar,
  NumberedSection,
  SummaryAffix,
} from "../../Components/Base/NumberedSection";
import { PanelTitle } from "../../Components/Base/PanelTitle";
import {
  useDatabaseCreate,
  useInstanceUpdate,
} from "../../Services/Databases/Hooks/useDatabaseMutations";
import useServiceInstances from "../../Services/Databases/Hooks/useServiceInstances";
import useServicesConfig from "../../Services/Databases/Hooks/useServicesConfig";
import {
  CredentialModel,
  DEDICATED_NAME_PATTERN,
  MAX_RESOURCE_NAME_LENGTH,
  RESOURCE_NAME_PATTERN,
  resourceWording,
  ServiceEngineModel,
  ServiceInstanceModel,
  ServiceVersionModel,
  ServiceOptionModel,
} from "../../Services/Databases/Models/databases.model";
import useMe from "../../Services/Users/Hooks/useMe";
import { UserProfile } from "../../Services/Users/Models/users.model";
import { getApiErrorMessage } from "../../Utils/apiErrors";
import ChoiceGroup, { Choice } from "./ChoiceCard";
import useNetworks from "../../Services/Networks/Hooks/useNetworks";
import { SYSTEM_NETWORK } from "../../Services/Networks/Models/networks.model";
import NetworkSelect from "../Networks/NetworkSelect";
import { NetworkLinks } from "../Networks/NetworkTags";
import CredentialsModal from "./CredentialsModal";
import ExposureModal from "./ExposureModal";
import InstanceAccess from "./InstanceAccess";
import InstanceNetworksModal from "./InstanceNetworksModal";
import ServiceSupportTags, { hasSupportInfo } from "./ServiceSupportTags";
import {
  engineLabel,
  formatSize,
  optionHelp,
  optionLabel,
  optionValueDescription,
  optionValueLabel,
} from "./serviceLabels";

const { Text, Paragraph } = Typography;

interface OptionPickerProps {
  engine: ServiceEngineModel;
  option: ServiceOptionModel;
  value: string;
  disabled: boolean;
  /** editing: the option was set at creation and cannot change */
  fixed?: boolean;
  onChange: (key: string, value: string) => void;
}

/** Value of the "custom size" card (never a real value: sizes are digits). */
const CUSTOM_CHOICE = "custom";

/** The values of one option, as cards. */
function OptionPicker({
  engine,
  option,
  value,
  disabled,
  fixed = false,
  onChange,
}: Readonly<OptionPickerProps>) {
  const { t, i18n } = useTranslation("databases");
  const lang = i18n.resolvedLanguage;
  const label = optionLabel(t, option.key);
  const help = optionHelp(t, option.key);
  const custom = option.custom;
  // Once "Custom" is chosen it stays selected, even when the size typed is
  // one of the offered values (the input must not vanish while typing).
  const [customMode, setCustomMode] = useState(
    () => !!custom && !option.values.includes(value),
  );
  const isCustom = !!custom && (customMode || !option.values.includes(value));
  const choices = useMemo<Choice[]>(
    () => [
      ...option.values.map((v) => {
        let description = optionValueDescription(t, option.key, v);
        // What a memory size gives the engine (half for Redis data...).
        if (option.key === "memory") {
          const mb = Number(v);
          if (engine.id === "redis" || engine.id === "valkey") {
            description = t("options.memory.detail.data", {
              size: optionValueLabel(t, "memory", String(mb / 2), lang),
            });
          } else if (engine.id === "nats") {
            description = t("options.memory.detail.streams", {
              size: optionValueLabel(t, "memory", String(mb / 4), lang),
            });
          }
        }
        return {
          value: v,
          title: optionValueLabel(t, option.key, v, lang),
          description,
          extra: v === option.default && (
            <Tag color="blue" style={{ width: "fit-content" }}>
              {t("createPage.default")}
            </Tag>
          ),
        };
      }),
      ...(option.custom
        ? [
            {
              value: CUSTOM_CHOICE,
              title: t("createPage.custom.title"),
              description: t("createPage.custom.description", {
                min: formatSize(option.custom.min, lang),
                max: formatSize(option.custom.max, lang),
              }),
            },
          ]
        : []),
    ],
    [engine.id, option, t, lang],
  );
  // A custom size starts from the largest offered one, doubled.
  const suggested = () => {
    const largest = Math.max(...option.values.map(Number));
    return String(Math.min(custom?.max ?? largest, largest * 2));
  };
  const onCustomGB = (gb: number | null) => {
    if (!custom || gb === null) return;
    const mb = Math.round((gb * 1024) / custom.step) * custom.step;
    onChange(
      option.key,
      String(Math.min(custom.max, Math.max(custom.min, mb))),
    );
  };
  return (
    <Flex vertical gap="small">
      <Flex vertical>
        <Flex gap="small" align="center" wrap>
          <Text strong>{label}</Text>
          {fixed && <Tag>{t("createPage.fixed")}</Tag>}
        </Flex>
        {help && <Text type="secondary">{help}</Text>}
      </Flex>
      <ChoiceGroup
        label={label}
        choices={choices}
        value={isCustom ? CUSTOM_CHOICE : value}
        disabled={disabled || fixed}
        columns={choices.length > 4 ? 3 : 4}
        onChange={(v) => {
          setCustomMode(v === CUSTOM_CHOICE);
          // Keeps a custom value already typed when coming back to Custom.
          if (v !== CUSTOM_CHOICE) onChange(option.key, v);
          else if (option.values.includes(value))
            onChange(option.key, suggested());
        }}
      />
      {isCustom && custom && (
        <InputNumber
          aria-label={t("createPage.custom.inputLabel", { option: label })}
          value={Number(value) / 1024}
          min={custom.min / 1024}
          max={custom.max / 1024}
          step={custom.step / 1024}
          // The step (e.g. 0.125) would otherwise show 3 decimals.
          formatter={(v) => (v === undefined ? "" : String(Number(v)))}
          disabled={disabled || fixed}
          suffix={t("createPage.custom.unit")}
          onChange={onCustomGB}
          style={{ width: 220 }}
        />
      )}
    </Flex>
  );
}

/**
 * Creation of a service resource, as a full page (like the sites). With a
 * name in the route, edits the options of a dedicated instance.
 */
/**
 * Creation (or edition) page, keyed by the engine: switching engines starts
 * from a fresh state (no network choice carried over to another engine).
 */
export default function CreateServicePage() {
  const { engine = "", name = "" } = useParams();
  return <CreateService key={`${engine}/${name}`} />;
}

/** The version of an instance, as a catalog entry (version no longer offered). */
function versionOfInstance(i: ServiceInstanceModel): ServiceVersionModel {
  return {
    version: i.version,
    image: i.image,
    platform: "",
    supportUntil: i.supportUntil,
    activeSupportUntil: i.activeSupportUntil,
    deprecated: i.deprecated,
  };
}

function CreateService() {
  const { t, i18n } = useTranslation("databases");
  const lang = i18n.resolvedLanguage;
  const { engine: engineParam = "", name: editName } = useParams();
  const editing = editName !== undefined;
  const navigate = useNavigate();
  const { token } = theme.useToken();
  const screens = Grid.useBreakpoint();
  const { data: me, isLoading: meLoading } = useMe();
  const isAdmin = me?.profile === UserProfile.ADMIN;
  const {
    data: config,
    isLoading,
    error: configError,
  } = useServicesConfig(isAdmin);
  const { data: instances } = useServiceInstances(config?.enabled ?? false);
  const { mutateAsync: create, isPending: creating } = useDatabaseCreate();
  const { mutateAsync: update, isPending: updating } = useInstanceUpdate();
  const isPending = creating || updating;
  const edited = editing
    ? (instances ?? []).find(
        (i) => i.engine === engineParam && i.name === editName,
      )
    : undefined;

  const engine = config?.engines?.find((e) => e.id === engineParam);
  const engineName = engineLabel(t, engineParam);
  const wording = resourceWording(engine, engineParam);
  const dedicated = engine?.lifecycle === "dedicated";
  const listPath = `/services/${encodeURIComponent(engineParam)}`;

  // The API lists the versions newest first.
  const defaultVersion =
    (engine?.versions.find((v) => !v.deprecated) ?? engine?.versions[0])
      ?.version ?? "";
  const [version, setVersion] = useState<string>();
  const selectedVersion = edited?.version ?? version ?? defaultVersion;
  const [options, setOptions] = useState<Record<string, string>>({});
  const [legacyAuth, setLegacyAuth] = useState(false);
  const { data: networkList, isLoading: networksLoading } = useNetworks(
    isAdmin && !!config?.enabled,
  );
  const [networksChoice, setNetworksChoice] = useState<string[]>();
  const [networksTouched, setNetworksTouched] = useState(false);
  const [networksTarget, setNetworksTarget] =
    useState<ServiceInstanceModel | null>(null);
  const [exposureTarget, setExposureTarget] =
    useState<ServiceInstanceModel | null>(null);
  const optionValue = (o: ServiceOptionModel) =>
    options[o.key] ?? edited?.options?.[o.key] ?? o.default;
  const [form] = Form.useForm<{ name: string }>();
  const typedName = Form.useWatch("name", form) ?? "";
  const name = editName ?? typedName;
  const [credentials, setCredentials] = useState<CredentialModel[] | null>(
    null,
  );
  const created = credentials !== null;
  const locked = isPending || created;

  const maxLength = useMemo(() => {
    if (!engine) return MAX_RESOURCE_NAME_LENGTH;
    const v = engine.versions.find((x) => x.version === selectedVersion);
    return Math.min(
      v?.maxNameLength ?? engine.maxNameLength,
      engine.maxNameLength || MAX_RESOURCE_NAME_LENGTH,
    );
  }, [engine, selectedVersion]);
  const namePattern = dedicated
    ? DEDICATED_NAME_PATTERN
    : RESOURCE_NAME_PATTERN;
  const nameInvalid = t(`wording.${wording}.nameInvalid`, { max: maxLength });

  const versionRunning = (v: string) =>
    !dedicated &&
    (instances ?? []).some((i) => i.engine === engineParam && i.version === v);
  const willStart = !dedicated && !versionRunning(selectedVersion);
  // Networks are chosen when the request creates the instance; a running
  // shared instance keeps its own (changed from the list).
  const networksApply = !editing && (dedicated || willStart);
  const runningShared = !dedicated
    ? (instances ?? []).find(
        (i) => i.engine === engineParam && i.version === selectedVersion,
      )
    : undefined;
  const unauthenticated = !!engine?.unauthenticated;
  const sharedAvailable = (networkList ?? []).some(
    (n) => n.name === SYSTEM_NETWORK,
  );
  const selectedNetworks =
    networksChoice ??
    (!unauthenticated && sharedAvailable ? [SYSTEM_NETWORK] : []);
  const networksError = !networksApply
    ? undefined
    : selectedNetworks.length === 0
      ? unauthenticated
        ? t("networks.privateRequired", { engine: engineName })
        : t("networks.required")
      : unauthenticated && selectedNetworks.includes(SYSTEM_NETWORK)
        ? t("networks.unauthenticatedShared")
        : undefined;
  // Same naming as the API (svc-<engine>-<name> / svc-<engine>-<version>).
  const host = dedicated
    ? `svc-${engineParam}-${name || "…"}`
    : `svc-${engineParam}-${selectedVersion.replace(/\./g, "-")}`;

  // Editing: only the version of the instance, shown even when the API no
  // longer offers it (the discovered catalog moved on): built from the
  // instance then.
  const versionList = useMemo<ServiceVersionModel[]>(() => {
    const offered = engine?.versions ?? [];
    if (!editing) return offered;
    if (!edited) return [];
    return [
      offered.find((v) => v.version === edited.version) ??
        versionOfInstance(edited),
    ];
  }, [engine, editing, edited]);

  const versionChoices = useMemo<Choice[]>(
    () =>
      versionList.map((v) => {
        const running =
          engine?.lifecycle !== "dedicated" &&
          (instances ?? []).some(
            (i) => i.engine === engineParam && i.version === v.version,
          );
        return {
          value: v.version,
          title: `${engineName} ${v.version}`,
          description: hasSupportInfo(v) ? (
            <ServiceSupportTags support={v} />
          ) : undefined,
          extra: (v.version === defaultVersion || running) && (
            <Flex gap={4} wrap>
              {v.version === defaultVersion && (
                <Tag color="blue">{t("createPage.recommended")}</Tag>
              )}
              {running && (
                <Tag color="green">{t("createPage.instanceRunning")}</Tag>
              )}
            </Flex>
          ),
        };
      }),
    [
      engine,
      instances,
      engineParam,
      engineName,
      defaultVersion,
      t,
      versionList,
    ],
  );

  const onVersion = (v: string) => {
    setVersion(v);
    // The name limit may be shorter on this version.
    if (form.getFieldValue("name")) {
      setTimeout(() => form.validateFields(["name"]).catch(() => {}));
    }
  };
  const onOption = (key: string, value: string) =>
    setOptions((prev) => ({ ...prev, [key]: value }));

  // Editing: the mutable options that differ from the instance's.
  const changes = Object.fromEntries(
    (engine?.options ?? [])
      .filter((o) => o.mutable && optionValue(o) !== edited?.options?.[o.key])
      .map((o) => [o.key, optionValue(o)]),
  );
  const save = () => {
    if (!edited || isPending) return;
    if (Object.keys(changes).length === 0) {
      toast.info(t("createPage.noChange"));
      return;
    }
    Modal.confirm({
      title: t("createPage.saveConfirm.title", { name: editName }),
      content: t("createPage.saveConfirm.content"),
      okText: t("actions.save", { ns: "common" }),
      cancelText: t("actions.cancel", { ns: "common" }),
      onOk: async () => {
        try {
          await update({ instance: edited.key, options: changes });
          toast.success(t("toasts.updated"));
          navigate(listPath);
        } catch (err) {
          toast.error(getApiErrorMessage(err));
        }
      },
    });
  };

  const submit = async () => {
    if (editing) {
      save();
      return;
    }
    if (!engine || !selectedVersion || locked) return;
    let values: { name: string };
    setNetworksTouched(true);
    try {
      values = await form.validateFields();
    } catch {
      return;
    }
    if (networksError) return;
    try {
      const res = await create({
        engine: engine.id,
        version: selectedVersion,
        name: values.name,
        options: dedicated
          ? Object.fromEntries(
              (engine.options ?? []).map((o) => [o.key, optionValue(o)]),
            )
          : undefined,
        legacyAuth: legacyAvailable && legacyAuth ? true : undefined,
        networks: networksApply ? selectedNetworks : undefined,
      });
      setCredentials(res.credentials);
      toast.success(t(`wording.${wording}.created`, { engine: engineName }));
    } catch (err) {
      toast.error(getApiErrorMessage(err));
    }
  };

  if (meLoading || (isAdmin && isLoading)) return <Skeleton />;
  if (!isAdmin) return <Result status="403" title={t("forbidden")} />;
  if (configError) {
    return (
      <Alert type="error" showIcon message={getApiErrorMessage(configError)} />
    );
  }
  const title = editing
    ? t("createPage.editTitle", { name: editName })
    : t(`wording.${wording}.createTitle`, { engine: engineName });
  if (
    !config?.enabled ||
    !engine ||
    (!editing && engine.versions.length === 0)
  ) {
    return (
      <Flex vertical gap="middle">
        <PanelTitle>{title}</PanelTitle>
        <Alert type="info" showIcon message={t("engineDisabled")} />
      </Flex>
    );
  }
  if (editing && !edited) {
    return (
      <Flex vertical gap="middle">
        <PanelTitle>{title}</PanelTitle>
        {instances ? (
          <Alert type="warning" showIcon message={t("createPage.notFound")} />
        ) : (
          <Skeleton />
        )}
      </Flex>
    );
  }

  const hasOptions = dedicated && (engine.options?.length ?? 0) > 0;
  // Older password mechanism, offered per version by the API.
  const legacyAvailable =
    !dedicated &&
    !!engine.versions.find((x) => x.version === selectedVersion)?.legacyAuth;
  const networksStep = hasOptions ? 3 : 2;
  const nameStep = networksStep + 1;
  const submitButton = (
    <Button
      type="primary"
      block
      size="large"
      loading={isPending}
      disabled={created || networksLoading}
      onClick={() => void submit()}
    >
      {editing ? t("actions.save", { ns: "common" }) : t("createPage.submit")}
    </Button>
  );

  return (
    <Flex vertical gap="middle" style={{ maxWidth: 1200 }}>
      <Flex gap="small" align="center">
        <Button
          type="text"
          icon={<ArrowLeftOutlined />}
          aria-label={t("createPage.back")}
          onClick={() => navigate(listPath)}
        />
        <PanelTitle>{title}</PanelTitle>
      </Flex>
      <Paragraph type="secondary" style={{ marginBottom: 0 }}>
        {editing ? t("createPage.editIntro") : t(`wording.${wording}.intro`)}
      </Paragraph>
      {engine.unauthenticated && (
        <Alert
          type="warning"
          showIcon
          message={t("createPage.unauthenticated")}
        />
      )}

      <Row gutter={[24, 24]}>
        <Col xs={24} lg={16}>
          <Flex vertical gap="middle">
            <NumberedSection
              step={1}
              title={t("createPage.sections.version.title")}
              subtitle={t("createPage.sections.version.subtitle")}
            >
              <ChoiceGroup
                label={t("createPage.sections.version.title")}
                choices={versionChoices}
                value={selectedVersion}
                disabled={locked || editing}
                onChange={onVersion}
              />
            </NumberedSection>

            {hasOptions && (
              <NumberedSection
                step={2}
                title={t("createPage.sections.options.title")}
                subtitle={t("createPage.sections.options.subtitle")}
              >
                <Flex vertical gap="large">
                  {engine.options?.map((o) => (
                    <OptionPicker
                      key={o.key}
                      engine={engine}
                      option={o}
                      value={optionValue(o)}
                      disabled={locked}
                      fixed={editing && !o.mutable}
                      onChange={onOption}
                    />
                  ))}
                </Flex>
              </NumberedSection>
            )}

            {editing && edited ? (
              <NumberedSection
                step={networksStep}
                title={t("networks.accessTitle")}
                subtitle={t("networks.accessSubtitle")}
              >
                <InstanceAccess
                  instance={edited}
                  onNetworks={() => setNetworksTarget(edited)}
                  onExposure={() => setExposureTarget(edited)}
                />
              </NumberedSection>
            ) : (
              <NumberedSection
                step={networksStep}
                title={t("networks.sectionTitle")}
                subtitle={t("networks.help")}
              >
                {networksApply ? (
                  <Flex vertical gap="small">
                    {unauthenticated && (
                      <Alert
                        type="warning"
                        showIcon
                        message={t("networks.privateRequired", {
                          engine: engineName,
                        })}
                      />
                    )}
                    <Form layout="vertical" requiredMark={false}>
                      <Form.Item
                        label={t("networks.label")}
                        htmlFor="service-networks"
                        validateStatus={
                          networksTouched && networksError ? "error" : undefined
                        }
                        help={networksTouched ? networksError : undefined}
                        style={{ marginBottom: 0 }}
                      >
                        <NetworkSelect
                          id="service-networks"
                          aria-label={t("networks.label")}
                          value={selectedNetworks}
                          onChange={(v) => {
                            setNetworksTouched(true);
                            setNetworksChoice(v);
                          }}
                          networks={networkList}
                          loading={networksLoading}
                          disabled={locked}
                          disabledReasons={
                            unauthenticated
                              ? {
                                  [SYSTEM_NETWORK]: t(
                                    "networks.unauthenticatedShared",
                                  ),
                                }
                              : undefined
                          }
                          showCreateLink
                          status={
                            networksTouched && networksError
                              ? "error"
                              : undefined
                          }
                        />
                      </Form.Item>
                    </Form>
                  </Flex>
                ) : (
                  <Flex vertical gap="small">
                    <Text type="secondary">
                      {t("networks.existingInstance", {
                        engine: engineName,
                        version: selectedVersion,
                      })}
                    </Text>
                    <NetworkLinks networks={runningShared?.networks} />
                  </Flex>
                )}
              </NumberedSection>
            )}

            {!editing && (
              <NumberedSection
                step={nameStep}
                title={t("createPage.sections.name.title")}
                subtitle={t(`wording.${wording}.nameHelp`, { max: maxLength })}
              >
                <Form
                  form={form}
                  layout="vertical"
                  disabled={locked}
                  requiredMark={false}
                  aria-label={t("createPage.sections.name.title")}
                >
                  <Form.Item
                    name="name"
                    label={t("createPage.nameLabel")}
                    rules={[
                      { required: true, message: t("create.nameRequired") },
                      { pattern: namePattern, message: nameInvalid },
                      { max: maxLength, message: nameInvalid },
                      {
                        validator: (_, value: string) =>
                          dedicated &&
                          (instances ?? []).some(
                            (i) => i.engine === engineParam && i.name === value,
                          )
                            ? Promise.reject(
                                new Error(t("createPage.nameTaken")),
                              )
                            : Promise.resolve(),
                      },
                    ]}
                    style={{ marginBottom: 0 }}
                  >
                    <Input
                      autoComplete="off"
                      spellCheck={false}
                      onPressEnter={() => void submit()}
                    />
                  </Form.Item>
                  {legacyAvailable && (
                    <Form.Item
                      label={t("createPage.legacyAuth.label")}
                      tooltip={t(
                        `createPage.legacyAuth.help.${engine.id === "postgres" ? "postgres" : "mysql"}`,
                      )}
                      style={{ marginTop: 16, marginBottom: 0 }}
                    >
                      <Switch
                        checked={legacyAuth}
                        onChange={setLegacyAuth}
                        aria-label={t("createPage.legacyAuth.label")}
                      />
                      {legacyAuth && (
                        <Alert
                          type="warning"
                          showIcon
                          style={{ marginTop: 8 }}
                          message={t("createPage.legacyAuth.warning")}
                        />
                      )}
                    </Form.Item>
                  )}
                </Form>
              </NumberedSection>
            )}
          </Flex>
        </Col>

        <Col xs={24} lg={8}>
          {/* The window scrolls, not the layout's content (which grows with
              the page): Affix follows the window, CSS sticky would not. */}
          <SummaryAffix enabled={!!screens.lg} offsetTop={token.margin}>
            <Card title={t("createPage.summary.title")}>
              <Flex vertical gap="middle">
                <Descriptions
                  column={1}
                  size="small"
                  items={[
                    {
                      key: "engine",
                      label: t("createPage.summary.engine"),
                      children: `${engineName} ${selectedVersion}`,
                    },
                    {
                      key: "type",
                      label: t("createPage.summary.type"),
                      children: dedicated
                        ? t("createPage.summary.dedicated")
                        : t("createPage.summary.shared"),
                    },
                    ...(dedicated
                      ? (engine.options ?? []).map((o) => ({
                          key: o.key,
                          label: optionLabel(t, o.key),
                          children: optionValueLabel(
                            t,
                            o.key,
                            optionValue(o),
                            lang,
                          ),
                        }))
                      : []),
                    {
                      key: "name",
                      label: t("createPage.summary.name"),
                      children: name || "—",
                    },
                    ...(legacyAvailable && legacyAuth
                      ? [
                          {
                            key: "auth",
                            label: t("createPage.legacyAuth.summary"),
                            children: t(
                              `createPage.legacyAuth.mechanism.${engine.id === "postgres" ? "postgres" : "mysql"}`,
                            ),
                          },
                        ]
                      : []),
                    ...(!editing
                      ? [
                          {
                            key: "networks",
                            label: t("networks.label"),
                            children: (
                              <NetworkLinks
                                networks={
                                  networksApply
                                    ? selectedNetworks
                                    : runningShared?.networks
                                }
                              />
                            ),
                          },
                        ]
                      : []),
                    {
                      key: "host",
                      label: t("createPage.summary.address"),
                      children: (
                        <Text code>{`${host}:${engine.defaultPort}`}</Text>
                      ),
                    },
                  ]}
                />
                <Alert
                  type={editing ? "warning" : "info"}
                  showIcon
                  message={
                    editing
                      ? t("createPage.summary.restartNote")
                      : dedicated
                        ? t("createPage.summary.dedicatedNote")
                        : willStart
                          ? t("createPage.summary.startNote", {
                              engine: engineName,
                              version: selectedVersion,
                            })
                          : t("createPage.summary.sharedNote", {
                              engine: engineName,
                              version: selectedVersion,
                            })
                  }
                />
                {!editing && !engine.unauthenticated && (
                  <Text type="secondary">
                    {t("createPage.summary.passwordNote")}
                  </Text>
                )}
                {screens.lg && submitButton}
                <Button
                  block
                  disabled={isPending}
                  onClick={() => navigate(listPath)}
                >
                  {t("actions.cancel", { ns: "common" })}
                </Button>
              </Flex>
            </Card>
          </SummaryAffix>
        </Col>
      </Row>

      {!screens.lg && <BottomActionBar>{submitButton}</BottomActionBar>}

      <CredentialsModal
        credentials={credentials}
        onClose={() => {
          setCredentials(null);
          navigate(listPath);
        }}
      />
      <InstanceNetworksModal
        instance={networksTarget}
        onClose={() => setNetworksTarget(null)}
      />
      <ExposureModal
        instance={exposureTarget}
        engine={engine}
        limits={config.exposure}
        onClose={() => setExposureTarget(null)}
      />
    </Flex>
  );
}
