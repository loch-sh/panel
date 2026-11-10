import {
  Button,
  Drawer,
  Flex,
  Form,
  Input,
  InputNumber,
  Select,
  Space,
  Switch,
} from "antd";
import cronstrue from "cronstrue/i18n";
import { useCallback, useEffect, useMemo } from "react";
import {
  CronJobCreatePayload,
  CronJobModel,
  CronJobType,
  DockerActionConfig,
  MAX_FUNCTION_EVENT_BYTES,
} from "../../Services/Cron/Models/cron.model";
import { ContainerInfo } from "dockerode";
import { useTranslation } from "react-i18next";
import { useCronstrueLocale } from "../../i18n/useLocales";

const jobTypeLabels = {
  [CronJobType.DOCKER_ACTION]: "jobTypes.dockerAction",
  [CronJobType.DOCKER_EXEC]: "jobTypes.dockerExec",
  [CronJobType.HTTP]: "jobTypes.httpRequest",
  [CronJobType.FUNCTION_INVOKE]: "jobTypes.functionInvoke",
} as const satisfies Record<CronJobType, string>;

const dockerActions = [
  { value: "start", label: "dockerActions.start" },
  { value: "stop", label: "dockerActions.stop" },
  { value: "restart", label: "dockerActions.restart" },
  { value: "kill", label: "dockerActions.kill" },
] as const satisfies readonly {
  value: DockerActionConfig["action"];
  label: string;
}[];

// Named descriptors supported by robfig/cron
const scheduleDescriptors = {
  "@yearly": "scheduleDescriptions.yearly",
  "@annually": "scheduleDescriptions.yearly",
  "@monthly": "scheduleDescriptions.monthly",
  "@weekly": "scheduleDescriptions.weekly",
  "@daily": "scheduleDescriptions.daily",
  "@midnight": "scheduleDescriptions.daily",
  "@hourly": "scheduleDescriptions.hourly",
} as const;

function isScheduleDescriptor(
  value: string,
): value is keyof typeof scheduleDescriptors {
  return value in scheduleDescriptors;
}

const httpMethods = ["GET", "POST", "PUT", "PATCH", "DELETE", "HEAD"];

interface Props {
  open: boolean;
  onClose: () => void;
  onSubmit: (values: CronJobCreatePayload & { id?: string }) => void;
  loading: boolean;
  job: CronJobModel | null;
  containers: ContainerInfo[];
  /** functions that can be invoked (the module is enabled, admins only) */
  functions?: string[];
  /** opens the creation of an invocation of this function */
  initialFunction?: string;
}

interface FormValues {
  name: string;
  schedule: string;
  type: CronJobType;
  enabled: boolean;
  once: boolean;
  containerName?: string;
  action?: string;
  command?: string;
  workingDir?: string;
  httpMethod?: string;
  httpUrl?: string;
  httpBody?: string;
  httpTimeout?: number;
  functionName?: string;
  functionEvent?: string;
}

/** Why an event is refused (one JSON value, at most 256 KiB). */
function eventError(value: string | undefined): "invalid" | "tooLarge" | null {
  const text = value?.trim() ? value : "{}";
  try {
    JSON.parse(text);
  } catch {
    return "invalid";
  }
  return new TextEncoder().encode(text).length > MAX_FUNCTION_EVENT_BYTES
    ? "tooLarge"
    : null;
}

export default function CronFormDrawer({
  open,
  onClose,
  onSubmit,
  loading,
  job,
  containers,
  functions,
  initialFunction,
}: Readonly<Props>) {
  const { t } = useTranslation(["cron", "common"]);
  const cronLocale = useCronstrueLocale();
  const [form] = Form.useForm<FormValues>();
  const jobType = Form.useWatch("type", form);
  const schedule = Form.useWatch("schedule", form);

  const cronDescription = useMemo(() => {
    if (!schedule?.trim()) return null;
    const trimmed = schedule.trim();

    // @every descriptors (robfig/cron specific)
    const everyMatch = trimmed.match(/^@every\s+(.+)$/);
    if (everyMatch) {
      return t("scheduleDescriptions.every", { interval: everyMatch[1] });
    }

    const descriptor = trimmed.toLowerCase();
    if (isScheduleDescriptor(descriptor)) {
      return t(scheduleDescriptors[descriptor]);
    }

    try {
      return cronstrue.toString(trimmed, { locale: cronLocale });
    } catch {
      return null;
    }
  }, [schedule, cronLocale, t]);

  useEffect(() => {
    if (open && job) {
      form.setFieldsValue({
        name: job.name,
        schedule: job.schedule,
        type: job.type,
        enabled: job.enabled,
        once: job.once,
        containerName:
          job.dockerAction?.containerName ?? job.dockerExec?.containerName,
        action: job.dockerAction?.action,
        command: job.dockerExec?.command?.join(" "),
        workingDir: job.dockerExec?.workingDir,
        httpMethod: job.http?.method,
        httpUrl: job.http?.url,
        httpBody: job.http?.body,
        httpTimeout: job.http?.timeout,
        functionName: job.functionInvoke?.function,
        functionEvent: job.functionInvoke?.event ?? "{}",
      });
    } else if (open) {
      form.resetFields();
      form.setFieldsValue({
        enabled: true,
        once: false,
        type: initialFunction
          ? CronJobType.FUNCTION_INVOKE
          : CronJobType.DOCKER_EXEC,
        httpMethod: "GET",
        functionName: initialFunction,
        functionEvent: "{}",
      });
    }
  }, [open, job, form, initialFunction]);

  const handleFinish = useCallback(
    (values: FormValues) => {
      const payload: CronJobCreatePayload & { id?: string } = {
        id: job?.id,
        name: values.name,
        schedule: values.schedule,
        type: values.type,
        enabled: values.enabled,
        once: values.once,
      };

      switch (values.type) {
        case CronJobType.DOCKER_ACTION:
          payload.dockerAction = {
            containerName: values.containerName!,
            action: values.action as DockerActionConfig["action"],
          };
          break;
        case CronJobType.DOCKER_EXEC:
          payload.dockerExec = {
            containerName: values.containerName!,
            command: values.command!.trim().split(/\s+/),
            workingDir: values.workingDir,
          };
          break;
        case CronJobType.HTTP:
          payload.http = {
            method: values.httpMethod!,
            url: values.httpUrl!,
            body: values.httpBody,
            timeout: values.httpTimeout,
          };
          break;
        case CronJobType.FUNCTION_INVOKE:
          payload.functionInvoke = {
            function: values.functionName!,
            event: values.functionEvent?.trim() ? values.functionEvent : "{}",
          };
          break;
      }

      onSubmit(payload);
    },
    [job, onSubmit],
  );

  // function_invoke is offered when the functions module is enabled, and
  // kept when editing such a job.
  const typeOptions = Object.values(CronJobType)
    .filter(
      (value) =>
        value !== CronJobType.FUNCTION_INVOKE ||
        functions !== undefined ||
        job?.type === CronJobType.FUNCTION_INVOKE,
    )
    .map((value) => ({ value, label: t(jobTypeLabels[value]) }));
  const functionOptions = [
    ...new Set([
      ...(functions ?? []),
      ...(job?.functionInvoke?.function ? [job.functionInvoke.function] : []),
    ]),
  ].map((name) => ({ value: name, label: name }));

  const containerOptions = containers.map((c) => ({
    value: c.Names?.[0]?.replace(/^\//, "") ?? c.Id.slice(0, 12),
    label: c.Names?.[0]?.replace(/^\//, "") ?? c.Id.slice(0, 12),
  }));

  return (
    <Drawer
      title={job ? t("form.title.edit") : t("form.title.create")}
      open={open}
      onClose={onClose}
      size="large"
      destroyOnHidden
    >
      <Form
        form={form}
        layout="vertical"
        onFinish={handleFinish}
        initialValues={{
          enabled: true,
          once: false,
          type: CronJobType.DOCKER_EXEC,
          httpMethod: "GET",
        }}
      >
        <Form.Item
          name="name"
          label={t("form.fields.name.label")}
          rules={[{ required: true, message: t("form.fields.name.required") }]}
        >
          <Input placeholder={t("form.fields.name.placeholder")} />
        </Form.Item>

        <Form.Item
          name="schedule"
          label={t("form.fields.schedule.label")}
          rules={[
            { required: true, message: t("form.fields.schedule.required") },
            {
              validator: (_, value) => {
                if (!value?.trim()) return Promise.resolve();
                const trimmed = value.trim();
                if (trimmed.startsWith("@")) return Promise.resolve();
                try {
                  cronstrue.toString(trimmed);
                  return Promise.resolve();
                } catch {
                  return Promise.reject(
                    new Error(t("form.fields.schedule.invalid")),
                  );
                }
              },
            },
          ]}
          help={cronDescription ?? t("form.fields.schedule.help")}
        >
          <Input placeholder="0 * * * *" style={{ fontFamily: "monospace" }} />
        </Form.Item>

        <Flex gap="middle">
          <Form.Item
            name="type"
            label={t("form.fields.type.label")}
            rules={[{ required: true }]}
            style={{ flex: 1 }}
          >
            <Select options={typeOptions} />
          </Form.Item>

          <Form.Item
            name="enabled"
            label={t("form.fields.enabled.label")}
            valuePropName="checked"
          >
            <Switch />
          </Form.Item>

          <Form.Item
            name="once"
            label={t("form.fields.once.label")}
            valuePropName="checked"
            tooltip={t("form.fields.once.tooltip")}
          >
            <Switch />
          </Form.Item>
        </Flex>

        {/* Docker Action */}
        {jobType === CronJobType.DOCKER_ACTION && (
          <>
            <Form.Item
              name="containerName"
              label={t("form.fields.container.label")}
              rules={[
                {
                  required: true,
                  message: t("form.fields.container.required"),
                },
              ]}
            >
              <Select
                showSearch={{ optionFilterProp: "label" }}
                placeholder={t("form.fields.container.placeholder")}
                options={containerOptions}
              />
            </Form.Item>
            <Form.Item
              name="action"
              label={t("form.fields.action.label")}
              rules={[
                { required: true, message: t("form.fields.action.required") },
              ]}
            >
              <Select
                options={dockerActions.map(({ value, label }) => ({
                  value,
                  label: t(label),
                }))}
              />
            </Form.Item>
          </>
        )}

        {/* Docker Exec */}
        {jobType === CronJobType.DOCKER_EXEC && (
          <>
            <Form.Item
              name="containerName"
              label={t("form.fields.container.label")}
              rules={[
                {
                  required: true,
                  message: t("form.fields.container.required"),
                },
              ]}
            >
              <Select
                showSearch={{ optionFilterProp: "label" }}
                placeholder={t("form.fields.container.placeholder")}
                options={containerOptions}
              />
            </Form.Item>
            <Form.Item
              name="command"
              label={t("form.fields.command.label")}
              rules={[
                { required: true, message: t("form.fields.command.required") },
              ]}
            >
              <Input
                placeholder="php bin/console cache:clear --env=prod"
                style={{ fontFamily: "monospace" }}
              />
            </Form.Item>
            <Form.Item
              name="workingDir"
              label={t("form.fields.workingDir.label")}
              help={t("form.fields.workingDir.help")}
            >
              <Input placeholder="/app" style={{ fontFamily: "monospace" }} />
            </Form.Item>
          </>
        )}

        {/* HTTP */}
        {jobType === CronJobType.HTTP && (
          <>
            <Flex gap="middle">
              <Form.Item
                name="httpMethod"
                label={t("form.fields.httpMethod.label")}
                rules={[
                  {
                    required: true,
                    message: t("form.fields.httpMethod.required"),
                  },
                ]}
                style={{ width: 130 }}
              >
                <Select
                  options={httpMethods.map((m) => ({ value: m, label: m }))}
                />
              </Form.Item>
              <Form.Item
                name="httpUrl"
                label={t("form.fields.httpUrl.label")}
                rules={[
                  {
                    required: true,
                    message: t("form.fields.httpUrl.required"),
                  },
                ]}
                style={{ flex: 1 }}
              >
                <Input placeholder="https://example.com/api/health" />
              </Form.Item>
            </Flex>
            <Form.Item name="httpBody" label={t("form.fields.httpBody.label")}>
              <Input.TextArea
                rows={3}
                placeholder='{"key": "value"}'
                style={{ fontFamily: "monospace" }}
              />
            </Form.Item>
            <Form.Item
              name="httpTimeout"
              label={t("form.fields.httpTimeout.label")}
            >
              <InputNumber min={1} max={300} placeholder="30" />
            </Form.Item>
          </>
        )}

        {/* Function invocation */}
        {jobType === CronJobType.FUNCTION_INVOKE && (
          <>
            <Form.Item
              name="functionName"
              label={t("form.fields.function.label")}
              rules={[
                {
                  required: true,
                  message: t("form.fields.function.required"),
                },
              ]}
              extra={t("form.fields.function.help")}
            >
              <Select
                showSearch={{ optionFilterProp: "label" }}
                placeholder={t("form.fields.function.placeholder")}
                options={functionOptions}
                notFoundContent={t("form.fields.function.none")}
              />
            </Form.Item>
            <Form.Item
              name="functionEvent"
              label={t("form.fields.functionEvent.label")}
              extra={t("form.fields.functionEvent.help")}
              rules={[
                {
                  validator: (_, value: string | undefined) => {
                    const problem = eventError(value);
                    return problem
                      ? Promise.reject(
                          new Error(t(`form.fields.functionEvent.${problem}`)),
                        )
                      : Promise.resolve();
                  },
                },
              ]}
            >
              <Input.TextArea
                rows={5}
                placeholder="{}"
                spellCheck={false}
                style={{ fontFamily: "monospace" }}
              />
            </Form.Item>
          </>
        )}

        <Form.Item style={{ marginTop: 50 }}>
          <Space>
            <Button type="primary" htmlType="submit" loading={loading}>
              {job
                ? t("form.submit.update")
                : t("actions.create", { ns: "common" })}
            </Button>
            <Button onClick={onClose}>
              {t("actions.cancel", { ns: "common" })}
            </Button>
          </Space>
        </Form.Item>
      </Form>
    </Drawer>
  );
}
