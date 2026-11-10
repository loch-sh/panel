import {
  Alert,
  Button,
  Card,
  Flex,
  Form,
  Input,
  Modal,
  Switch,
  Typography,
} from "antd";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { toast } from "react-toastify";
import { useFunctionUpdate } from "../../../Services/Functions/Hooks/useFunctionMutations";
import {
  EnvVarModel,
  FunctionDetailModel,
  FunctionLimits,
  FunctionsInfoModel,
  BOOTSTRAP_FILE,
  handlerValid as isHandlerValid,
  isCustomRuntime,
  UpdateFunctionPayload,
} from "../../../Services/Functions/Models/functions.model";
import { getApiErrorMessage } from "../../../Utils/apiErrors";
import EnvVarsEditor, { envRowsValid } from "../EnvVarsEditor";
import { handlerHelpKey } from "../functionLabels";
import ResourcesFields, { limitsProblem } from "../ResourcesFields";

const limitsOf = (fn: FunctionDetailModel): FunctionLimits => ({
  memoryMB: fn.memoryMB,
  cpus: fn.cpus,
  timeoutSeconds: fn.timeoutSeconds,
  tmpSizeMB: fn.tmpSizeMB,
});

const sameEnv = (a: readonly EnvVarModel[], b: readonly EnvVarModel[]) =>
  a.length === b.length &&
  a.every((x, i) => x.name === b[i].name && x.value === b[i].value);

/** Resources, handler and variables (a running function is recreated). */
export default function ConfigurationTab({
  fn,
  config,
}: Readonly<{ fn: FunctionDetailModel; config: FunctionsInfoModel }>) {
  const { t } = useTranslation("functions");
  const { mutateAsync: update, isPending } = useFunctionUpdate();
  const [limits, setLimits] = useState<FunctionLimits>(() => limitsOf(fn));
  const [handler, setHandler] = useState(fn.handler);
  const [env, setEnv] = useState<EnvVarModel[]>(() => fn.env ?? []);
  const [submitted, setSubmitted] = useState(false);
  const [mcp, setMcp] = useState(fn.mcpInvocable ?? false);
  // Defensive: an older API without the field hides the toggle.
  const mcpAvailable = typeof fn.mcpInvocable === "boolean";

  const rows = env.filter((r) => r.name || r.value);
  const custom = isCustomRuntime(fn.runtime);
  const handlerValid = isHandlerValid(fn.runtime, handler);
  const envValid = envRowsValid(t, rows, fn.secrets ?? []);
  const limitsInvalid = limitsProblem(limits, config) !== null;

  const changes = (): UpdateFunctionPayload => {
    const current = limitsOf(fn);
    const out: UpdateFunctionPayload = {};
    for (const key of Object.keys(current) as (keyof FunctionLimits)[]) {
      if (limits[key] !== current[key]) out[key] = limits[key];
    }
    // "" resets the handler of the custom runtime to bootstrap.
    if (
      handler !== fn.handler &&
      !(custom && handler === "" && fn.handler === BOOTSTRAP_FILE)
    ) {
      out.handler = handler;
    }
    if (!sameEnv(rows, fn.env ?? [])) out.env = rows;
    if (mcpAvailable && mcp !== fn.mcpInvocable) out.mcpInvocable = mcp;
    return out;
  };

  const save = () => {
    setSubmitted(true);
    if (!handlerValid || !envValid || limitsInvalid) return;
    const payload = changes();
    if (Object.keys(payload).length === 0) {
      toast.info(t("configuration.noChange"));
      return;
    }
    Modal.confirm({
      title: t("configuration.confirm.title", { name: fn.name }),
      content:
        fn.state === "running"
          ? t("configuration.confirm.running")
          : t("configuration.confirm.stopped"),
      okText: t("actions.save", { ns: "common" }),
      cancelText: t("actions.cancel", { ns: "common" }),
      onOk: async () => {
        try {
          await update({ name: fn.name, ...payload });
          toast.success(t("toasts.updated", { name: fn.name }));
          setSubmitted(false);
        } catch (err) {
          toast.error(getApiErrorMessage(err));
        }
      },
    });
  };

  return (
    <Flex vertical gap="middle">
      <Card title={t("configuration.resources")}>
        <ResourcesFields
          value={limits}
          onChange={setLimits}
          config={config}
          current={limitsOf(fn)}
          disabled={isPending}
        />
      </Card>
      <Card title={t("configuration.code")}>
        <Flex vertical gap="middle">
          <Alert type="info" showIcon title={t("configuration.deployScope")} />
          <Form layout="vertical" component="div" disabled={isPending}>
            <Form.Item
              label={
                custom
                  ? t("create.handlerOptionalLabel")
                  : t("create.handlerLabel")
              }
              htmlFor="fn-config-handler"
              validateStatus={!handlerValid ? "error" : undefined}
              help={
                !handlerValid
                  ? t(
                      custom
                        ? "create.handlerInvalidCustom"
                        : "create.handlerInvalid",
                    )
                  : t(handlerHelpKey(fn.runtime))
              }
            >
              <Input
                id="fn-config-handler"
                value={handler}
                placeholder={custom ? BOOTSTRAP_FILE : undefined}
                onChange={(e) => setHandler(e.target.value.trim())}
                autoComplete="off"
                spellCheck={false}
                style={{ fontFamily: "monospace" }}
              />
            </Form.Item>
          </Form>
          <Flex vertical gap="small">
            <Typography.Text strong>{t("env.variable.title")}</Typography.Text>
            <Typography.Text type="secondary">
              {t("env.variable.help")}
            </Typography.Text>
            <EnvVarsEditor
              value={env}
              onChange={setEnv}
              otherNames={fn.secrets ?? []}
              disabled={isPending}
              showErrors={submitted}
              used={(fn.secrets ?? []).length}
            />
          </Flex>
        </Flex>
      </Card>
      {mcpAvailable && (
        <Card title={t("configuration.mcp.title")}>
          <Flex vertical gap="small">
            <Flex gap="small" align="center">
              <Switch
                checked={mcp}
                onChange={setMcp}
                disabled={isPending}
                aria-label={t("configuration.mcp.label")}
              />
              <Typography.Text>{t("configuration.mcp.label")}</Typography.Text>
            </Flex>
            <Typography.Text type="secondary">
              {t("configuration.mcp.help")}
            </Typography.Text>
          </Flex>
        </Card>
      )}
      <Flex gap="small">
        <Button type="primary" loading={isPending} onClick={save}>
          {t("actions.save", { ns: "common" })}
        </Button>
        <Button
          disabled={isPending}
          onClick={() => {
            setLimits(limitsOf(fn));
            setHandler(fn.handler);
            setEnv(fn.env ?? []);
            setMcp(fn.mcpInvocable ?? false);
            setSubmitted(false);
          }}
        >
          {t("configuration.reset")}
        </Button>
      </Flex>
    </Flex>
  );
}
