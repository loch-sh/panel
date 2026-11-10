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
  Result,
  Row,
  Select,
  Space,
  Tag,
  Typography,
  theme,
} from "antd";
import { useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { useNavigate } from "react-router";
import { toast } from "react-toastify";
import {
  BottomActionBar,
  NumberedSection,
  SummaryAffix,
} from "../../Components/Base/NumberedSection";
import { PanelTitle } from "../../Components/Base/PanelTitle";
import useServiceInstances from "../../Services/Databases/Hooks/useServiceInstances";
import useEnabledFunctions from "../../Services/Functions/Hooks/useEnabledFunctions";
import useServicesConfig from "../../Services/Databases/Hooks/useServicesConfig";
import useHostingConfig from "../../Services/Hosting/Hooks/useHostingConfig";
import useHostingStacks from "../../Services/Hosting/Hooks/useHostingStacks";
import {
  useNetworkAttach,
  useNetworkCreate,
} from "../../Services/Networks/Hooks/useNetworkMutations";
import useNetworks from "../../Services/Networks/Hooks/useNetworks";
import useNetworksConfig from "../../Services/Networks/Hooks/useNetworksConfig";
import {
  NETWORK_DESCRIPTION_MAX_LENGTH,
  NETWORK_NAME_MAX_LENGTH,
  NETWORK_NAME_PATTERN,
  RESERVED_NETWORK_NAMES,
} from "../../Services/Networks/Models/networks.model";
import {
  parseIPv4Prefix,
  PRIVATE_RANGES,
  SubnetRules,
  subnetProblem,
  usableHosts,
} from "../../Services/Networks/subnet";
import useMe from "../../Services/Users/Hooks/useMe";
import { UserProfile } from "../../Services/Users/Models/users.model";
import { getApiErrorMessage } from "../../Utils/apiErrors";
import ChoiceGroup, { Choice } from "../Databases/ChoiceCard";
import { decodeMember, memberOptions } from "./memberOptions";
import {
  CreatedNetworkState,
  NETWORKS_PATH,
  networkPath,
} from "./networkPaths";

const { Text, Paragraph } = Typography;

type Addressing = "automatic" | "manual";

interface FormValues {
  name: string;
  description?: string;
  subnet?: string;
  /** encoded members ("site/<name>", "instance/<key>") to attach */
  members?: string[];
}

/** No member is declared on a network that does not exist yet. */
const NO_MEMBER: ReadonlySet<string> = new Set();

/** Same Docker name as the API. */
const dockerNameOf = (name: string) => `loch-net-${name || "…"}`;

/** The API refuses control characters in a description. */
const hasControlChars = (s: string) =>
  [...s].some((c) => c.charCodeAt(0) < 0x20 || c.charCodeAt(0) === 0x7f);

/** IPv4 ranges as code tags. */
function RangeTags({
  ranges,
  color,
}: Readonly<{ ranges: readonly string[]; color?: string }>) {
  return (
    <Space size={4} wrap>
      {ranges.map((r) => (
        <Tag key={r} color={color} style={{ fontFamily: "monospace" }}>
          {r}
        </Tag>
      ))}
    </Space>
  );
}

/** Creation of a private network, as a full page (like the services). */
export default function CreateNetwork() {
  const { t } = useTranslation("networks");
  const { t: tdb } = useTranslation("databases");
  const navigate = useNavigate();
  const { token } = theme.useToken();
  const screens = Grid.useBreakpoint();
  const { data: me, isLoading: meLoading } = useMe();
  const isAdmin = me?.profile === UserProfile.ADMIN;
  const { data: networks } = useNetworks(isAdmin);
  const { data: config } = useNetworksConfig(isAdmin);
  const rules = useMemo<SubnetRules>(
    () =>
      config
        ? {
            privateRanges: config.privateRanges,
            reservedRanges: config.reservedRanges,
            minPrefix: config.minPrefix,
            maxPrefix: config.maxPrefix,
          }
        : {},
    [config],
  );
  const reservedRanges = config?.reservedRanges ?? [];
  const privateRanges = config?.privateRanges?.length
    ? config.privateRanges
    : PRIVATE_RANGES;
  const limitReached = !!config && config.max > 0 && config.count >= config.max;
  const { mutateAsync: create, isPending: creating } = useNetworkCreate();
  const { mutateAsync: attach, isPending: attaching } = useNetworkAttach();
  const isPending = creating || attaching;
  const { data: hostingConfig } = useHostingConfig(isAdmin);
  const { data: stacks } = useHostingStacks(
    isAdmin && (hostingConfig?.enabled ?? false),
  );
  const { data: servicesConfig } = useServicesConfig(isAdmin);
  const { data: instances } = useServiceInstances(
    isAdmin && (servicesConfig?.enabled ?? false),
  );
  const functions = useEnabledFunctions(isAdmin);
  const memberChoices = useMemo(
    () =>
      memberOptions(
        t,
        tdb,
        stacks ?? [],
        instances ?? [],
        { system: false, declared: NO_MEMBER },
        functions,
      ),
    [t, tdb, stacks, instances, functions],
  );
  const memberTitle = (value: string) =>
    memberChoices.flatMap((g) => g.options).find((o) => o.value === value)
      ?.title ?? decodeMember(value).name;
  const [form] = Form.useForm<FormValues>();
  const members: string[] = Form.useWatch("members", form) ?? [];
  const name = Form.useWatch("name", form) ?? "";
  const subnet = (Form.useWatch("subnet", form) ?? "").trim();
  const [addressing, setAddressing] = useState<Addressing>("automatic");
  const [created, setCreated] = useState(false);
  const locked = isPending || created;

  const addressingChoices = useMemo<Choice[]>(
    () =>
      (["automatic", "manual"] as const).map((v) => ({
        value: v,
        title: t(`create.addressing.${v}.title`),
        description: t(`create.addressing.${v}.description`),
      })),
    [t],
  );

  const subnetValid = subnet !== "" && subnetProblem(subnet, rules) === null;
  const hosts = subnetValid ? usableHosts(parseIPv4Prefix(subnet)!.bits) : 0;

  const submit = async () => {
    if (locked || limitReached) return;
    let values: FormValues;
    try {
      values = await form.validateFields();
    } catch {
      return;
    }
    try {
      const network = await create({
        name: values.name,
        description: values.description?.trim() || undefined,
        subnet:
          addressing === "manual"
            ? values.subnet?.trim() || undefined
            : undefined,
      });
      setCreated(true);
      // No batch in the API: one attach per member, in order. A site is
      // redeployed asynchronously: the detail page follows its operation.
      const state: Required<CreatedNetworkState> = {
        pendingSites: {},
        failedMembers: [],
      };
      for (const target of (values.members ?? []).map(decodeMember)) {
        try {
          const res = await attach({ network: network.name, ...target });
          if (target.kind === "site" && res.operation) {
            state.pendingSites[target.name] = res.operation;
          }
        } catch (err) {
          state.failedMembers.push({
            ...target,
            message: getApiErrorMessage(err),
          });
        }
      }
      if (state.failedMembers.length > 0) {
        toast.warning(
          t("create.members.partial", {
            name: network.name,
            count: state.failedMembers.length,
          }),
        );
      } else {
        toast.success(t("toasts.created", { name: network.name }));
      }
      navigate(networkPath(network.name), { state });
    } catch (err) {
      toast.error(getApiErrorMessage(err));
    }
  };

  if (meLoading) return null;
  if (!isAdmin) return <Result status="403" title={t("forbidden")} />;

  const submitButton = (
    <Button
      type="primary"
      block
      size="large"
      loading={isPending}
      disabled={created || limitReached}
      onClick={() => void submit()}
    >
      {t("create.submit")}
    </Button>
  );

  return (
    <Flex vertical gap="middle" style={{ maxWidth: 1200 }}>
      <Flex gap="small" align="center">
        <Button
          type="text"
          icon={<ArrowLeftOutlined />}
          aria-label={t("create.back")}
          onClick={() => navigate(NETWORKS_PATH)}
        />
        <PanelTitle>{t("create.title")}</PanelTitle>
      </Flex>
      <Paragraph type="secondary" style={{ marginBottom: 0 }}>
        {t("create.intro")}
      </Paragraph>
      {limitReached && (
        <Alert
          type="error"
          showIcon
          message={t("create.limitReached", { max: config?.max })}
        />
      )}

      <Form
        form={form}
        layout="vertical"
        disabled={locked}
        requiredMark={false}
        initialValues={{ name: "", description: "", subnet: "", members: [] }}
        onFinish={() => void submit()}
      >
        <Row gutter={[24, 24]}>
          <Col xs={24} lg={16}>
            <Flex vertical gap="middle">
              <NumberedSection
                step={1}
                title={t("create.sections.name.title")}
                subtitle={t("create.sections.name.subtitle")}
              >
                <Form.Item
                  name="name"
                  label={t("create.nameLabel")}
                  validateTrigger="onChange"
                  extra={
                    <Text type="secondary">
                      {t("create.dockerName", { name: dockerNameOf(name) })}
                    </Text>
                  }
                  rules={[
                    { required: true, message: t("create.nameRequired") },
                    {
                      max: NETWORK_NAME_MAX_LENGTH,
                      message: t("create.nameInvalid"),
                    },
                    {
                      pattern: NETWORK_NAME_PATTERN,
                      message: t("create.nameInvalid"),
                    },
                    {
                      validator: (_, value: string) =>
                        RESERVED_NETWORK_NAMES.includes(value)
                          ? Promise.reject(new Error(t("create.nameReserved")))
                          : Promise.resolve(),
                    },
                    {
                      validator: (_, value: string) =>
                        (networks ?? []).some((n) => n.name === value)
                          ? Promise.reject(new Error(t("create.nameTaken")))
                          : Promise.resolve(),
                    },
                  ]}
                  style={{ marginBottom: 0 }}
                >
                  <Input
                    autoComplete="off"
                    spellCheck={false}
                    placeholder={t("create.namePlaceholder")}
                    maxLength={NETWORK_NAME_MAX_LENGTH + 8}
                  />
                </Form.Item>
              </NumberedSection>

              <NumberedSection
                step={2}
                title={t("create.sections.description.title")}
                subtitle={t("create.sections.description.subtitle")}
              >
                <Form.Item
                  name="description"
                  label={t("create.descriptionLabel")}
                  rules={[
                    {
                      validator: (_, value?: string) =>
                        value &&
                        (hasControlChars(value) ||
                          new TextEncoder().encode(value.trim()).length >
                            NETWORK_DESCRIPTION_MAX_LENGTH)
                          ? Promise.reject(
                              new Error(t("create.descriptionInvalid")),
                            )
                          : Promise.resolve(),
                    },
                  ]}
                  style={{ marginBottom: 0 }}
                >
                  <Input.TextArea
                    autoSize={{ minRows: 2, maxRows: 4 }}
                    showCount
                    maxLength={NETWORK_DESCRIPTION_MAX_LENGTH}
                    placeholder={t("create.descriptionPlaceholder")}
                  />
                </Form.Item>
              </NumberedSection>

              <NumberedSection
                step={3}
                title={t("create.sections.addressing.title")}
                subtitle={t("create.sections.addressing.subtitle")}
              >
                <Flex vertical gap="middle">
                  <ChoiceGroup
                    label={t("create.sections.addressing.title")}
                    choices={addressingChoices}
                    value={addressing}
                    disabled={locked}
                    columns={4}
                    onChange={(v) => setAddressing(v as Addressing)}
                  />
                  {addressing === "manual" && (
                    <Form.Item
                      name="subnet"
                      label={t("create.subnetLabel")}
                      validateTrigger="onChange"
                      extra={
                        <Text type="secondary">
                          {subnetValid
                            ? t("create.usableHosts", { count: hosts })
                            : t("create.subnetHelp")}
                        </Text>
                      }
                      rules={[
                        {
                          validator: (_, value?: string) => {
                            const v = (value ?? "").trim();
                            if (!v) {
                              return Promise.reject(
                                new Error(t("create.subnetRequired")),
                              );
                            }
                            const p = subnetProblem(v, rules);
                            return p
                              ? Promise.reject(
                                  new Error(
                                    t(`create.subnetProblems.${p.problem}`, {
                                      expected: p.expected,
                                      range: p.range,
                                    }),
                                  ),
                                )
                              : Promise.resolve();
                          },
                        },
                      ]}
                      style={{ marginBottom: 0 }}
                    >
                      <Input
                        autoComplete="off"
                        spellCheck={false}
                        placeholder={t("create.subnetPlaceholder")}
                        style={{ maxWidth: 280 }}
                      />
                    </Form.Item>
                  )}
                  <Descriptions
                    column={1}
                    size="small"
                    items={[
                      {
                        key: "private",
                        label: t("create.ranges.private"),
                        children: <RangeTags ranges={privateRanges} />,
                      },
                      {
                        key: "reserved",
                        label: t("create.ranges.reserved"),
                        children:
                          reservedRanges.length > 0 ? (
                            <RangeTags ranges={reservedRanges} color="orange" />
                          ) : (
                            <Text type="secondary">
                              {t("create.ranges.reservedNone")}
                            </Text>
                          ),
                      },
                    ]}
                  />
                  {reservedRanges.length > 0 && (
                    <Text type="secondary">
                      {t(
                        addressing === "manual"
                          ? "create.ranges.reservedHintManual"
                          : "create.ranges.reservedHintAutomatic",
                      )}
                    </Text>
                  )}
                </Flex>
              </NumberedSection>

              <NumberedSection
                step={4}
                title={t("create.sections.members.title")}
                subtitle={t("create.sections.members.subtitle")}
              >
                <Flex vertical gap="middle">
                  <Form.Item
                    name="members"
                    label={t("create.members.label")}
                    htmlFor="create-members"
                    style={{ marginBottom: 0 }}
                  >
                    <Select
                      id="create-members"
                      mode="multiple"
                      allowClear
                      showSearch={{ optionFilterProp: "title" }}
                      options={memberChoices}
                      optionLabelProp="title"
                      placeholder={t("create.members.placeholder")}
                      notFoundContent={t("attach.noResource")}
                    />
                  </Form.Item>
                  {members.length > 0 && (
                    <Alert
                      type="warning"
                      showIcon
                      message={t("create.members.restart")}
                    />
                  )}
                </Flex>
              </NumberedSection>
            </Flex>
          </Col>

          <Col xs={24} lg={8}>
            <SummaryAffix enabled={!!screens.lg} offsetTop={token.margin}>
              <Card title={t("create.summary.title")}>
                <Flex vertical gap="middle">
                  <Descriptions
                    column={1}
                    size="small"
                    items={[
                      {
                        key: "name",
                        label: t("create.summary.name"),
                        children: name || "—",
                      },
                      {
                        key: "docker",
                        label: t("create.summary.dockerName"),
                        children: <Text code>{dockerNameOf(name)}</Text>,
                      },
                      {
                        key: "subnet",
                        label: t("create.summary.subnet"),
                        children:
                          addressing === "manual" && subnet ? (
                            <Text code>{subnet}</Text>
                          ) : (
                            t("automatic")
                          ),
                      },
                      {
                        key: "internet",
                        label: t("create.summary.internet"),
                        children: t("create.summary.internetNone"),
                      },
                      {
                        key: "members",
                        label: t("create.summary.members"),
                        children:
                          members.length > 0 ? (
                            <Space size={4} wrap>
                              {members.map((m) => (
                                <Tag key={m}>{memberTitle(m)}</Tag>
                              ))}
                            </Space>
                          ) : (
                            t("create.summary.membersNone")
                          ),
                      },
                      ...(config && config.max > 0
                        ? [
                            {
                              key: "limit",
                              label: t("create.summary.limit"),
                              children: (
                                <Text
                                  type={limitReached ? "danger" : undefined}
                                >
                                  {t("create.summary.limitValue", {
                                    used: config.count,
                                    max: config.max,
                                  })}
                                </Text>
                              ),
                            },
                          ]
                        : []),
                    ]}
                  />
                  <Alert
                    type="info"
                    showIcon
                    message={t("create.summary.note")}
                  />
                  {screens.lg && submitButton}
                  <Button
                    block
                    disabled={isPending}
                    onClick={() => navigate(NETWORKS_PATH)}
                  >
                    {t("actions.cancel", { ns: "common" })}
                  </Button>
                </Flex>
              </Card>
            </SummaryAffix>
          </Col>
        </Row>
      </Form>

      {!screens.lg && <BottomActionBar>{submitButton}</BottomActionBar>}
    </Flex>
  );
}
