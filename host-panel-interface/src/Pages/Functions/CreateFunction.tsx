import {
  ArrowLeftOutlined,
  CheckCircleOutlined,
  CloseCircleOutlined,
  LoadingOutlined,
  PaperClipOutlined,
} from "@ant-design/icons";
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
  List,
  Progress,
  Row,
  Switch,
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
import useFunctions from "../../Services/Functions/Hooks/useFunctions";
import {
  useFunctionCreate,
  useFunctionDeploy,
  useFunctionSecretSet,
} from "../../Services/Functions/Hooks/useFunctionMutations";
import {
  BOOTSTRAP_FILE,
  dedicatedNetwork,
  defaultHandler,
  EnvVarModel,
  FUNCTION_NAME_MAX_LENGTH,
  FUNCTION_NAME_PATTERN,
  FunctionCredentialsModel,
  FunctionLimits,
  FunctionsInfoModel,
  goBuildCommand,
  handlerValid as isHandlerValid,
  isCustomRuntime,
  RESERVED_FUNCTION_NAMES,
} from "../../Services/Functions/Models/functions.model";
import useNetworks from "../../Services/Networks/Hooks/useNetworks";
import { SYSTEM_NETWORK } from "../../Services/Networks/Models/networks.model";
import { getApiErrorMessage } from "../../Utils/apiErrors";
import { formatBytes } from "../../Utils/bytes";
import ChoiceGroup, { Choice } from "../Databases/ChoiceCard";
import NetworkSelect from "../Networks/NetworkSelect";
import { NetworkLinks } from "../Networks/NetworkTags";
import EnvVarsEditor, { envRowsValid } from "./EnvVarsEditor";
import FunctionCredentialsModal from "./FunctionCredentialsModal";
import {
  deploymentWarnings,
  formatMB,
  functionPath,
  FUNCTIONS_PATH,
  handlerHelpKey,
  runtimeDescriptionKey,
  runtimeLabel,
  runtimeVersionLabel,
} from "./functionLabels";
import ResourcesFields, { limitsProblem } from "./ResourcesFields";
import TrademarkNotice from "./TrademarkNotice";
import useFunctionsGate from "./useFunctionsGate";
import ZipDropzone from "./ZipDropzone";

const { Text, Paragraph } = Typography;

type StepState = "pending" | "running" | "done" | "failed";

interface SetupStep {
  key: "secrets" | "code";
  state: StepState;
  progress?: number;
  error?: string;
}

function StepIcon({ state }: Readonly<{ state: StepState }>) {
  const { token } = theme.useToken();
  switch (state) {
    case "done":
      return <CheckCircleOutlined style={{ color: token.colorSuccess }} />;
    case "failed":
      return <CloseCircleOutlined style={{ color: token.colorError }} />;
    default:
      return (
        <LoadingOutlined style={{ opacity: state === "pending" ? 0.4 : 1 }} />
      );
  }
}

/** Rows typed but left empty are dropped. */
const filled = (rows: EnvVarModel[]) => rows.filter((r) => r.name || r.value);

/** The Invoke URL the API gives the function (members of its networks). */
export const invokeEndpoint = (name: string) =>
  `http://fn-${name}:8080/2015-03-31/functions/${name}/invocations`;

/** Creation of a function, as a full page (like the services). */
export default function CreateFunction() {
  const { t } = useTranslation("functions");
  const { blocked, config } = useFunctionsGate(t("create.title"));
  if (blocked) return blocked;
  return config ? <CreateFunctionForm config={config} /> : null;
}

function CreateFunctionForm({
  config,
}: Readonly<{ config: FunctionsInfoModel }>) {
  const { t } = useTranslation("functions");
  const navigate = useNavigate();
  const { token } = theme.useToken();
  const screens = Grid.useBreakpoint();
  const { data: existing } = useFunctions();
  const { data: networkList, isLoading: networksLoading } = useNetworks();
  const {
    mutateAsync: create,
    isPending: creating,
    reset: resetCreate,
  } = useFunctionCreate();
  const { mutateAsync: deploy } = useFunctionDeploy();
  const { mutateAsync: setSecret, reset: resetSecret } = useFunctionSecretSet();

  const runtimes = config.runtimes;
  const [runtime, setRuntime] = useState(runtimes[0]?.id ?? "");
  const versions = useMemo(
    () => runtimes.find((r) => r.id === runtime)?.versions ?? [],
    [runtimes, runtime],
  );
  const [version, setVersion] = useState<string>();
  const selectedVersion =
    version && versions.includes(version) ? version : (versions[0] ?? "");
  const [handler, setHandler] = useState<string>();
  const selectedHandler = handler ?? defaultHandler(runtime);
  const [zip, setZip] = useState<File | null>(null);
  const [limits, setLimits] = useState<FunctionLimits>(config.defaults);
  const [env, setEnv] = useState<EnvVarModel[]>([]);
  const [secrets, setSecrets] = useState<EnvVarModel[]>([]);
  const [networks, setNetworks] = useState<string[]>([]);
  const [egress, setEgress] = useState(false);
  const [sigv4, setSigv4] = useState(false);
  const [form] = Form.useForm<{ name: string }>();
  const name: string = Form.useWatch("name", form) ?? "";
  const [submitted, setSubmitted] = useState(false);
  const [credentials, setCredentials] =
    useState<FunctionCredentialsModel | null>(null);
  const [createdName, setCreatedName] = useState("");
  const [steps, setSteps] = useState<SetupStep[]>([]);
  const created = credentials !== null;
  const locked = creating || created;

  const custom = isCustomRuntime(runtime);
  const handlerValid = isHandlerValid(runtime, selectedHandler);
  const envRows = filled(env);
  const secretRows = filled(secrets);
  const envValid =
    envRowsValid(
      t,
      envRows,
      secretRows.map((s) => s.name),
    ) &&
    envRowsValid(
      t,
      secretRows,
      envRows.map((e) => e.name),
    );
  const limitsInvalid = limitsProblem(limits, config) !== null;
  const full = config.count >= config.max;

  const runtimeChoices = useMemo<Choice[]>(
    () =>
      runtimes.map((r) => ({
        value: r.id,
        title: runtimeLabel(t, r.id),
        description: t(runtimeDescriptionKey(r.id)),
      })),
    [runtimes, t],
  );
  const versionChoices = useMemo<Choice[]>(
    () =>
      versions.map((v, i) => ({
        value: v,
        title: runtimeVersionLabel(t, runtime, v),
        extra: i === 0 && (
          <Tag color="blue" style={{ width: "fit-content" }}>
            {t("create.latest")}
          </Tag>
        ),
      })),
    [versions, runtime, t],
  );

  const updateStep = (key: SetupStep["key"], patch: Partial<SetupStep>) =>
    setSteps((prev) =>
      prev.map((s) => (s.key === key ? { ...s, ...patch } : s)),
    );

  /** Secrets, then the code: after the creation (the API takes neither). */
  const runSetup = async (
    fn: string,
    secretList: EnvVarModel[],
    file: File | null,
  ) => {
    if (secretList.length > 0) {
      updateStep("secrets", { state: "running" });
      try {
        for (const s of secretList) {
          await setSecret({ name: fn, key: s.name, value: s.value });
        }
        updateStep("secrets", { state: "done" });
      } catch (err) {
        updateStep("secrets", {
          state: "failed",
          error: getApiErrorMessage(err),
        });
      }
    }
    if (file) {
      updateStep("code", { state: "running", progress: 0 });
      try {
        const d = await deploy({
          name: fn,
          file,
          onProgress: (progress) => updateStep("code", { progress }),
        });
        updateStep("code", { state: "done", progress: 100 });
        for (const w of deploymentWarnings(t, d)) toast.warning(w);
      } catch (err) {
        updateStep("code", { state: "failed", error: getApiErrorMessage(err) });
      }
    }
  };

  const submit = async () => {
    if (locked) return;
    setSubmitted(true);
    let values: { name: string };
    try {
      values = await form.validateFields();
    } catch {
      return;
    }
    if (!handlerValid || !envValid || limitsInvalid || !selectedVersion) return;
    const secretList = secretRows;
    const file = zip;
    try {
      const res = await create({
        name: values.name,
        runtime,
        runtimeVersion: selectedVersion,
        // The custom runtime defaults to bootstrap.
        handler: selectedHandler || undefined,
        memoryMB: limits.memoryMB,
        cpus: limits.cpus,
        timeoutSeconds: limits.timeoutSeconds,
        tmpSizeMB: limits.tmpSizeMB,
        env: envRows.length > 0 ? envRows : undefined,
        networks: networks.length > 0 ? networks : undefined,
        egress: egress || undefined,
        sigv4: sigv4 || undefined,
      });
      // The secret values are not kept in the page once sent.
      setSecrets([]);
      setCreatedName(values.name);
      setSteps([
        ...(secretList.length > 0
          ? [{ key: "secrets" as const, state: "pending" as const }]
          : []),
        ...(file ? [{ key: "code" as const, state: "pending" as const }] : []),
      ]);
      setCredentials(res.credentials);
      toast.success(t("toasts.created", { name: values.name }));
      void runSetup(values.name, secretList, file);
    } catch (err) {
      toast.error(getApiErrorMessage(err));
    }
  };

  const setupRunning = steps.some(
    (s) => s.state === "pending" || s.state === "running",
  );
  const own = dedicatedNetwork(name || "…");
  const shownNetworks = [own, ...networks];

  const submitButton = (
    <Button
      type="primary"
      block
      size="large"
      loading={creating}
      disabled={created || full || !config.runtimeReady}
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
          onClick={() => navigate(FUNCTIONS_PATH)}
        />
        <PanelTitle>{t("create.title")}</PanelTitle>
      </Flex>
      <Paragraph type="secondary" style={{ marginBottom: 0 }}>
        {t("create.intro")}
      </Paragraph>
      {!config.runtimeReady && (
        <Alert type="warning" showIcon title={t("runtimeNotReady")} />
      )}
      {full && (
        <Alert
          type="warning"
          showIcon
          title={t("limitReached", { max: config.max })}
        />
      )}

      <Row gutter={[24, 24]}>
        <Col xs={24} lg={16}>
          <Flex vertical gap="middle">
            <NumberedSection
              step={1}
              title={t("create.sections.name.title")}
              subtitle={t("create.sections.name.subtitle", {
                max: FUNCTION_NAME_MAX_LENGTH,
              })}
            >
              <Form
                form={form}
                layout="vertical"
                disabled={locked}
                requiredMark={false}
                aria-label={t("create.sections.name.title")}
              >
                <Form.Item
                  name="name"
                  label={t("create.nameLabel")}
                  rules={[
                    { required: true, message: t("create.nameRequired") },
                    {
                      pattern: FUNCTION_NAME_PATTERN,
                      message: t("create.nameInvalid"),
                    },
                    {
                      validator: (_, value: string) =>
                        RESERVED_FUNCTION_NAMES.includes(value)
                          ? Promise.reject(new Error(t("create.nameReserved")))
                          : (existing ?? []).some((f) => f.name === value)
                            ? Promise.reject(new Error(t("create.nameTaken")))
                            : Promise.resolve(),
                    },
                  ]}
                  style={{ marginBottom: 0 }}
                >
                  <Input
                    autoComplete="off"
                    spellCheck={false}
                    maxLength={FUNCTION_NAME_MAX_LENGTH}
                  />
                </Form.Item>
              </Form>
            </NumberedSection>

            <NumberedSection
              step={2}
              title={t("create.sections.runtime.title")}
              subtitle={t("create.sections.runtime.subtitle")}
            >
              <Flex vertical gap="middle">
                <ChoiceGroup
                  label={t("create.runtimeLabel")}
                  choices={runtimeChoices}
                  value={runtime}
                  disabled={locked}
                  onChange={(r) => {
                    setRuntime(r);
                    setVersion(undefined);
                    setHandler(undefined);
                  }}
                />
                <ChoiceGroup
                  label={t("create.versionLabel")}
                  choices={versionChoices}
                  value={selectedVersion}
                  disabled={locked}
                  columns={3}
                  onChange={setVersion}
                />
                <Text type="secondary">
                  {t("create.imageNote", { arch: config.arch })}
                </Text>
              </Flex>
            </NumberedSection>

            <NumberedSection
              step={3}
              title={t("create.sections.code.title")}
              subtitle={t("create.sections.code.subtitle")}
            >
              <Form layout="vertical" disabled={locked} component="div">
                {custom && (
                  <Alert
                    type="info"
                    showIcon
                    style={{ marginBottom: token.margin }}
                    title={t("create.bootstrap.title")}
                    description={
                      <Flex vertical gap="small">
                        <Text>
                          {t("create.bootstrap.help", { arch: config.arch })}
                        </Text>
                        <Text
                          code
                          copyable
                          style={{ wordBreak: "break-all" }}
                          data-testid="fn-build-command"
                        >
                          {goBuildCommand(config.arch)}
                        </Text>
                      </Flex>
                    }
                  />
                )}
                <Form.Item
                  label={
                    custom
                      ? t("create.handlerOptionalLabel")
                      : t("create.handlerLabel")
                  }
                  htmlFor="fn-handler"
                  validateStatus={!handlerValid ? "error" : undefined}
                  help={
                    !handlerValid
                      ? t(
                          custom
                            ? "create.handlerInvalidCustom"
                            : "create.handlerInvalid",
                        )
                      : t(handlerHelpKey(runtime))
                  }
                >
                  <Input
                    id="fn-handler"
                    value={selectedHandler}
                    placeholder={custom ? BOOTSTRAP_FILE : undefined}
                    onChange={(e) => setHandler(e.target.value.trim())}
                    autoComplete="off"
                    spellCheck={false}
                    style={{ fontFamily: "monospace" }}
                  />
                </Form.Item>
                <Form.Item
                  label={t("create.zipLabel")}
                  style={{ marginBottom: 0 }}
                >
                  {zip ? (
                    <Flex gap="small" align="center" wrap>
                      <PaperClipOutlined />
                      <Text>{zip.name}</Text>
                      <Text type="secondary">{formatBytes(zip.size)}</Text>
                      <Button
                        size="small"
                        disabled={locked}
                        onClick={() => setZip(null)}
                      >
                        {t("create.zipRemove")}
                      </Button>
                    </Flex>
                  ) : (
                    <ZipDropzone
                      maxBytes={config.maxCodeBytes}
                      disabled={locked}
                      onFile={setZip}
                    />
                  )}
                  <Text
                    type="secondary"
                    style={{ display: "block", marginTop: 8 }}
                  >
                    {t("create.zipOptional")}
                  </Text>
                </Form.Item>
              </Form>
            </NumberedSection>

            <NumberedSection
              step={4}
              title={t("create.sections.resources.title")}
              subtitle={t("create.sections.resources.subtitle")}
            >
              <ResourcesFields
                value={limits}
                onChange={setLimits}
                config={config}
                disabled={locked}
              />
            </NumberedSection>

            <NumberedSection
              step={5}
              title={t("create.sections.env.title")}
              subtitle={t("create.sections.env.subtitle")}
            >
              <Flex vertical gap="middle">
                <Flex vertical gap="small">
                  <Text strong>{t("env.variable.title")}</Text>
                  <Text type="secondary">{t("env.variable.help")}</Text>
                  <EnvVarsEditor
                    value={env}
                    onChange={setEnv}
                    otherNames={secretRows.map((s) => s.name)}
                    disabled={locked}
                    showErrors={submitted}
                    used={secrets.length}
                  />
                </Flex>
                <Flex vertical gap="small">
                  <Text strong>{t("env.secret.title")}</Text>
                  <Alert
                    type="warning"
                    showIcon
                    title={t("env.secret.warning")}
                  />
                  <EnvVarsEditor
                    value={secrets}
                    onChange={setSecrets}
                    secret
                    otherNames={envRows.map((e) => e.name)}
                    disabled={locked}
                    showErrors={submitted}
                    used={env.length}
                  />
                </Flex>
              </Flex>
            </NumberedSection>

            <NumberedSection
              step={6}
              title={t("create.sections.networks.title")}
              subtitle={t("create.sections.networks.subtitle")}
            >
              <Flex vertical gap="middle">
                <Flex vertical gap={4}>
                  <Text strong>{t("networks.dedicated")}</Text>
                  <Flex gap="small" align="center" wrap>
                    <Tag color="purple">{own}</Tag>
                    <Text type="secondary">{t("networks.dedicatedHint")}</Text>
                  </Flex>
                </Flex>
                <Form layout="vertical" component="div">
                  <Form.Item
                    label={t("networks.extra")}
                    htmlFor="fn-networks"
                    help={t("networks.extraHelp")}
                    style={{ marginBottom: 0 }}
                  >
                    <NetworkSelect
                      id="fn-networks"
                      aria-label={t("networks.extra")}
                      value={networks}
                      onChange={setNetworks}
                      networks={networkList}
                      loading={networksLoading}
                      disabled={locked}
                      disabledReasons={{
                        [SYSTEM_NETWORK]: t("networks.servicesRefused"),
                      }}
                      showCreateLink
                    />
                  </Form.Item>
                </Form>
                <Flex vertical gap="small">
                  <Flex gap="small" align="center">
                    <Switch
                      checked={egress}
                      onChange={setEgress}
                      disabled={locked}
                      aria-label={t("egress.label")}
                    />
                    <Text>{t("egress.label")}</Text>
                  </Flex>
                  <Text type="secondary">{t("egress.help")}</Text>
                  {egress && (
                    <Alert
                      type="warning"
                      showIcon
                      title={t("egress.warning")}
                    />
                  )}
                </Flex>
              </Flex>
            </NumberedSection>

            <NumberedSection
              step={7}
              title={t("create.sections.invoke.title")}
              subtitle={t("create.sections.invoke.subtitle")}
            >
              <Flex vertical gap="small">
                <Text>{t("sigv4.bearer")}</Text>
                <Flex gap="small" align="center">
                  <Switch
                    checked={sigv4}
                    onChange={setSigv4}
                    disabled={locked}
                    aria-label={t("sigv4.label")}
                  />
                  <Text>{t("sigv4.label")}</Text>
                </Flex>
                <Text type="secondary">{t("sigv4.help")}</Text>
                {sigv4 && (
                  <Alert type="warning" showIcon title={t("sigv4.warning")} />
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
                      key: "runtime",
                      label: t("create.summary.runtime"),
                      children: runtimeVersionLabel(
                        t,
                        runtime,
                        selectedVersion,
                      ),
                    },
                    {
                      key: "handler",
                      label: t("create.summary.handler"),
                      children: (
                        <Text code>
                          {selectedHandler || (custom ? BOOTSTRAP_FILE : "—")}
                        </Text>
                      ),
                    },
                    {
                      key: "code",
                      label: t("create.summary.code"),
                      children: zip ? zip.name : t("create.summary.codeLater"),
                    },
                    {
                      key: "resources",
                      label: t("create.summary.resources"),
                      children: t("create.summary.resourcesValue", {
                        memory: Number.isFinite(limits.memoryMB)
                          ? formatMB(limits.memoryMB)
                          : "—",
                        cpus: Number.isFinite(limits.cpus) ? limits.cpus : "—",
                        timeout: Number.isFinite(limits.timeoutSeconds)
                          ? limits.timeoutSeconds
                          : "—",
                      }),
                    },
                    {
                      key: "env",
                      label: t("create.summary.env"),
                      children: t("create.summary.envValue", {
                        variables: envRows.length,
                        secrets: secretRows.length,
                      }),
                    },
                    {
                      key: "networks",
                      label: t("create.summary.networks"),
                      children: (
                        <NetworkLinks
                          networks={name ? shownNetworks : networks}
                        />
                      ),
                    },
                    {
                      key: "internet",
                      label: t("create.summary.internet"),
                      children: egress ? t("egress.on") : t("egress.off"),
                    },
                    {
                      key: "auth",
                      label: t("create.summary.auth"),
                      children: sigv4
                        ? t("create.summary.authSigv4")
                        : t("create.summary.authBearer"),
                    },
                    {
                      key: "endpoint",
                      label: t("create.summary.endpoint"),
                      children: (
                        <Text code style={{ wordBreak: "break-all" }}>
                          {invokeEndpoint(name || "…")}
                        </Text>
                      ),
                    },
                  ]}
                />
                <Text type="secondary">
                  {t("create.summary.credentialsNote")}
                </Text>
                {screens.lg && submitButton}
                <Button
                  block
                  disabled={creating}
                  onClick={() => navigate(FUNCTIONS_PATH)}
                >
                  {t("actions.cancel", { ns: "common" })}
                </Button>
              </Flex>
            </Card>
          </SummaryAffix>
        </Col>
      </Row>
      <TrademarkNotice />

      {!screens.lg && <BottomActionBar>{submitButton}</BottomActionBar>}

      <FunctionCredentialsModal
        credentials={credentials}
        closeDisabled={setupRunning}
        onClose={() => {
          setCredentials(null);
          // The credentials and the secret values leave the mutations' state.
          resetCreate();
          resetSecret();
          navigate(functionPath(createdName));
        }}
      >
        {steps.length > 0 && (
          <List
            size="small"
            style={{ marginTop: 16 }}
            header={<Text strong>{t("create.setup.title")}</Text>}
            dataSource={steps}
            renderItem={(s) => (
              <List.Item>
                <Flex vertical gap={4} style={{ width: "100%" }}>
                  <Flex gap="small" align="center">
                    <StepIcon state={s.state} />
                    <Text>{t(`create.setup.${s.key}`)}</Text>
                  </Flex>
                  {s.key === "code" && s.state === "running" && (
                    <Progress percent={s.progress ?? 0} size="small" />
                  )}
                  {s.error && (
                    <Text type="danger">
                      {t("create.setup.failed", { error: s.error })}
                    </Text>
                  )}
                </Flex>
              </List.Item>
            )}
          />
        )}
      </FunctionCredentialsModal>
    </Flex>
  );
}
