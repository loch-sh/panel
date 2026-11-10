import {
  CaretRightOutlined,
  DeleteOutlined,
  EditFilled,
  PauseCircleOutlined,
  PlayCircleOutlined,
  PlusOutlined,
} from "@ant-design/icons";
import { Button, Flex, Modal, Space, Tag, Tooltip, Typography } from "antd";
import Table, { ColumnsType } from "antd/es/table";
import { useCallback, useEffect, useMemo, useState } from "react";
import { useSearchParams } from "react-router";
import useFunctions from "../../Services/Functions/Hooks/useFunctions";
import useFunctionsConfig from "../../Services/Functions/Hooks/useFunctionsConfig";
import useMe from "../../Services/Users/Hooks/useMe";
import { UserProfile } from "../../Services/Users/Models/users.model";
import { PanelTitle } from "../../Components/Base/PanelTitle";
import useCronJobs from "../../Services/Cron/Hooks/useCronJobs";
import useCronCreate from "../../Services/Cron/Hooks/useCronCreate";
import useCronUpdate from "../../Services/Cron/Hooks/useCronUpdate";
import useCronDelete from "../../Services/Cron/Hooks/useCronDelete";
import useCronPatch from "../../Services/Cron/Hooks/useCronPatch";
import useCronRun from "../../Services/Cron/Hooks/useCronRun";
import useContainers from "../../Services/Containers/Hooks/useContainers";
import {
  CronJobCreatePayload,
  CronJobModel,
  CronJobStatus,
  CronJobType,
  parseFunctionInvokeOutput,
} from "../../Services/Cron/Models/cron.model";
import CronFormDrawer from "./CronFormDrawer";
import { getApiErrorMessage } from "../../Utils/apiErrors";
import { format, Locale } from "date-fns";
import type { TFunction } from "i18next";
import { useTranslation } from "react-i18next";
import { toast } from "react-toastify";
import { useDateFnsLocale } from "../../i18n/useLocales";

const { Text } = Typography;

type CronT = TFunction<["cron", "common"]>;

const typeConfig = {
  [CronJobType.DOCKER_ACTION]: { label: "jobTypes.dockerAction", color: "blue" },
  [CronJobType.DOCKER_EXEC]: { label: "jobTypes.dockerExec", color: "purple" },
  [CronJobType.HTTP]: { label: "jobTypes.http", color: "cyan" },
  [CronJobType.FUNCTION_INVOKE]: {
    label: "jobTypes.function",
    color: "magenta",
  },
} as const satisfies Record<CronJobType, { label: string; color: string }>;

const statusConfig = {
  [CronJobStatus.PENDING]: { label: "statuses.pending", color: "default" },
  [CronJobStatus.RUNNING]: { label: "statuses.running", color: "processing" },
  [CronJobStatus.SUCCESS]: { label: "statuses.success", color: "success" },
  [CronJobStatus.ERROR]: { label: "statuses.error", color: "error" },
} as const satisfies Record<CronJobStatus, { label: string; color: string }>;

function formatDate(locale: Locale, dateStr?: string): string {
  if (!dateStr) return "—";
  return format(new Date(dateStr), "PP pp", { locale });
}

function getJobTarget(job: CronJobModel): string {
  switch (job.type) {
    case CronJobType.DOCKER_ACTION:
      return `${job.dockerAction?.action} → ${job.dockerAction?.containerName}`;
    case CronJobType.DOCKER_EXEC:
      return job.dockerExec?.command?.join(" ") ?? "";
    case CronJobType.HTTP:
      return `${job.http?.method} ${job.http?.url}`;
    case CronJobType.FUNCTION_INVOKE:
      return `λ ${job.functionInvoke?.function ?? ""} ${job.functionInvoke?.event ?? "{}"}`;
    default:
      return "";
  }
}

const DISABLED_REASONS = { FUNCTION_DELETED: "disabledReasons.FUNCTION_DELETED" } as const;

/** Why the API disabled a job (an unknown reason is shown as is). */
function disabledReasonLabel(t: CronT, reason: string): string {
  return reason in DISABLED_REASONS
    ? t(DISABLED_REASONS[reason as keyof typeof DISABLED_REASONS])
    : reason;
}

/** The summary of a function invocation (never its payload). */
function FunctionRunSummary({ output }: Readonly<{ output: string }>) {
  const { t } = useTranslation(["cron", "common"]);
  const summary = parseFunctionInvokeOutput(output);
  if (!summary) {
    return <Text style={{ fontFamily: "monospace", fontSize: 12 }}>{output}</Text>;
  }
  const ok =
    summary.statusCode >= 200 && summary.statusCode < 300 && !summary.functionError;
  return (
    <Space size={4} wrap>
      <Tag color={ok ? "green" : "red"}>{summary.statusCode}</Tag>
      <Text>
        {t("table.details.functionRun", {
          duration: summary.durationMs,
          bytes: summary.payloadBytes,
        })}
      </Text>
      {summary.functionError && (
        <Text type="danger">
          {t("table.details.functionError", { error: summary.functionError })}
        </Text>
      )}
    </Space>
  );
}

function getColumns(params: {
  t: CronT;
  dateLocale: Locale;
  onEdit: (job: CronJobModel) => void;
  onDelete: (id: string) => void;
  onRun: (id: string, name: string) => void;
  onToggleEnabled: (job: CronJobModel) => void;
  disableActions: boolean;
}): ColumnsType<CronJobModel> {
  const { t } = params;
  return [
    {
      title: t("table.columns.name"),
      dataIndex: "name",
      width: 200,
    },
    {
      title: t("table.columns.schedule"),
      dataIndex: "schedule",
      width: 150,
      responsive: ["md"],
      render: (value: string) => (
        <Text code style={{ fontSize: 12 }}>
          {value}
        </Text>
      ),
    },
    {
      title: t("table.columns.type"),
      dataIndex: "type",
      width: 140,
      responsive: ["md"],
      render: (value: CronJobType) => {
        const cfg = typeConfig[value] ?? { label: value, color: "default" };
        return <Tag color={cfg.color}>{t(cfg.label as never)}</Tag>;
      },
    },
    {
      title: t("table.columns.target"),
      responsive: ["md"],
      render: (_: unknown, record: CronJobModel) => (
        <Text
          ellipsis={{ tooltip: getJobTarget(record) }}
          style={{ maxWidth: 250, fontFamily: "monospace", fontSize: 12 }}
        >
          {getJobTarget(record)}
        </Text>
      ),
    },
    {
      title: t("table.columns.mode"),
      width: 120,
      responsive: ["md"],
      render: (_: unknown, record: CronJobModel) => {
        if (!record.enabled) {
          return record.disabledReason ? (
            <Tooltip title={disabledReasonLabel(t, record.disabledReason)}>
              <Tag color="red">{t("modes.disabledByPanel")}</Tag>
            </Tooltip>
          ) : (
            <Tag color="default">{t("modes.disabled")}</Tag>
          );
        }
        if (record.once) {
          return <Tag color="orange">{t("modes.once")}</Tag>;
        }
        return <Tag color="green">{t("modes.recurring")}</Tag>;
      },
    },
    {
      title: t("table.columns.lastStatus"),
      dataIndex: "lastStatus",
      width: 120,
      responsive: ["md"],
      render: (value: CronJobStatus, record: CronJobModel) => {
        const cfg = statusConfig[value];
        return (
          <Space size={4} wrap>
            <Tag color={cfg.color}>{t(cfg.label)}</Tag>
            {record.lastSkipped && (
              <Tooltip
                title={t("table.details.lastSkipped", {
                  date: formatDate(params.dateLocale, record.lastSkipped),
                })}
              >
                <Tag color="orange">{t("statuses.skipped")}</Tag>
              </Tooltip>
            )}
          </Space>
        );
      },
    },
    {
      title: t("table.columns.lastRun"),
      dataIndex: "lastRun",
      width: 180,
      responsive: ["md"],
      render: (value: string) => (
        <Text style={{ fontSize: 12 }}>{formatDate(params.dateLocale, value)}</Text>
      ),
    },
    {
      title: t("table.columns.actions"),
      width: 180,
      render: (_: unknown, record: CronJobModel) => (
        <Space.Compact>
          <Tooltip title={t("actions.edit", { ns: "common" })}>
            <Button
              disabled={params.disableActions}
              shape="circle"
              type="primary"
              variant="outlined"
              onClick={() => params.onEdit(record)}
              icon={<EditFilled />}
            />
          </Tooltip>
          <Tooltip title={t("table.actions.runNow")}>
            <Button
              disabled={
                params.disableActions ||
                record.lastStatus === CronJobStatus.RUNNING
              }
              shape="circle"
              color="primary"
              variant="outlined"
              onClick={() => params.onRun(record.id, record.name)}
              icon={<CaretRightOutlined />}
            />
          </Tooltip>
          <Tooltip
            title={
              record.enabled
                ? t("table.actions.suspend")
                : t("table.actions.enable")
            }
          >
            <Button
              disabled={params.disableActions}
              shape="circle"
              color={record.enabled ? "orange" : "green"}
              variant="outlined"
              onClick={() => params.onToggleEnabled(record)}
              icon={
                record.enabled ? (
                  <PauseCircleOutlined />
                ) : (
                  <PlayCircleOutlined />
                )
              }
            />
          </Tooltip>
          <Tooltip title={t("actions.delete", { ns: "common" })}>
            <Button
              danger
              disabled={params.disableActions}
              shape="circle"
              onClick={() => params.onDelete(record.id)}
              icon={<DeleteOutlined />}
            />
          </Tooltip>
        </Space.Compact>
      ),
    },
  ];
}

export default function Cron() {
  const { t } = useTranslation(["cron", "common"]);
  const dateLocale = useDateFnsLocale();
  const { data: jobs, isLoading } = useCronJobs();
  const { data: containers } = useContainers();
  const { data: me } = useMe();
  const isAdmin = me?.profile === UserProfile.ADMIN;
  const { data: functionsConfig } = useFunctionsConfig(isAdmin);
  const functionsEnabled = !!functionsConfig?.enabled;
  const { data: functionList } = useFunctions(functionsEnabled);
  const functionNames = useMemo(
    () => (functionsEnabled ? (functionList ?? []).map((f) => f.name) : undefined),
    [functionsEnabled, functionList],
  );
  // "?newFunction=<name>": from a function's Triggers tab.
  const [searchParams, setSearchParams] = useSearchParams();
  const newFunction = searchParams.get("newFunction") ?? undefined;
  const { mutateAsync: createJob, isPending: creating } = useCronCreate();
  const { mutateAsync: updateJob, isPending: updating } = useCronUpdate();
  const { mutateAsync: patchJob, isPending: patching } = useCronPatch();
  const { mutateAsync: deleteJob, isPending: deleting } = useCronDelete();
  const { mutateAsync: runJob, isPending: running } = useCronRun();

  const isPending = creating || updating || patching || deleting || running;

  const [drawerOpen, setDrawerOpen] = useState(false);
  const [jobToEdit, setJobToEdit] = useState<CronJobModel | null>(null);

  const [initialFunction, setInitialFunction] = useState<string>();
  const openCreate = useCallback(() => {
    setJobToEdit(null);
    setInitialFunction(undefined);
    setDrawerOpen(true);
  }, []);
  useEffect(() => {
    if (!newFunction) return;
    setJobToEdit(null);
    setInitialFunction(newFunction);
    setDrawerOpen(true);
    setSearchParams({}, { replace: true });
  }, [newFunction, setSearchParams]);

  const openEdit = useCallback((job: CronJobModel) => {
    setJobToEdit(job);
    setDrawerOpen(true);
  }, []);

  const closeDrawer = useCallback(() => {
    setDrawerOpen(false);
    setJobToEdit(null);
  }, []);

  const handleSubmit = useCallback(
    async (values: CronJobCreatePayload & { id?: string }) => {
      try {
        if (values.id) {
          await updateJob({ ...values, id: values.id });
        } else {
          await createJob(values);
        }
        closeDrawer();
      } catch (err) {
        toast.error(getApiErrorMessage(err));
      }
    },
    [createJob, updateJob, closeDrawer]
  );

  const handleDelete = useCallback(
    (id: string) => {
      Modal.confirm({
        title: t("confirm.delete.title"),
        content: t("confirm.delete.content"),
        okText: t("actions.delete", { ns: "common" }),
        okButtonProps: { danger: true },
        cancelText: t("actions.cancel", { ns: "common" }),
        onOk: () => deleteJob(id).catch((err) => toast.error(getApiErrorMessage(err))),
      });
    },
    [deleteJob, t],
  );

  const handleToggleEnabled = useCallback(
    (job: CronJobModel) => {
      const texts = job.enabled
        ? {
            title: t("confirm.suspend.title"),
            content: t("confirm.suspend.content", { name: job.name }),
            okText: t("confirm.suspend.ok"),
          }
        : {
            title: t("confirm.enable.title"),
            content: t("confirm.enable.content", { name: job.name }),
            okText: t("confirm.enable.ok"),
          };
      Modal.confirm({
        ...texts,
        cancelText: t("actions.cancel", { ns: "common" }),
        onOk: () => patchJob({ id: job.id, enabled: !job.enabled }).catch((err) => toast.error(getApiErrorMessage(err))),
      });
    },
    [patchJob, t],
  );

  const handleRun = useCallback(
    (id: string, name: string) => {
      Modal.confirm({
        title: t("confirm.run.title"),
        content: t("confirm.run.content", { name }),
        okText: t("confirm.run.ok"),
        cancelText: t("actions.cancel", { ns: "common" }),
        onOk: () => runJob(id).catch((err) => toast.error(getApiErrorMessage(err))),
      });
    },
    [runJob, t],
  );

  const expandedRowRender = useCallback((record: CronJobModel) => {
    if (!record.lastOutput && !record.lastError && !record.disabledReason) {
      return null;
    }

    return (
      <Flex vertical gap="small" style={{ padding: "8px 0" }}>
        {record.disabledReason && (
          <Text type="warning">{disabledReasonLabel(t, record.disabledReason)}</Text>
        )}
        {record.lastError && (
          <div>
            <Text strong style={{ color: "var(--ant-color-error)" }}>
              {t("table.details.error")}{" "}
            </Text>
            <Text style={{ fontFamily: "monospace", fontSize: 12 }}>
              {record.lastError}
            </Text>
          </div>
        )}
        {record.lastOutput && record.type === CronJobType.FUNCTION_INVOKE && (
          <div>
            <Text strong>{t("table.details.output")} </Text>
            <FunctionRunSummary output={record.lastOutput} />
          </div>
        )}
        {record.lastOutput && record.type !== CronJobType.FUNCTION_INVOKE && (
          <div>
            <Text strong>{t("table.details.output")}</Text>
            <pre
              style={{
                margin: "4px 0 0",
                padding: 8,
                fontSize: 12,
                maxHeight: 200,
                overflow: "auto",
                background: "var(--ant-color-fill-tertiary)",
                borderRadius: 4,
              }}
            >
              {record.lastOutput}
            </pre>
          </div>
        )}
      </Flex>
    );
  }, [t]);

  return (
    <>
      <Flex gap="middle" vertical>
        <Flex justify="space-between" align="center">
          <PanelTitle>{t("title")}</PanelTitle>
          <Button
            type="primary"
            icon={<PlusOutlined />}
            onClick={openCreate}
          >
            {t("newJob")}
          </Button>
        </Flex>

        <Table<CronJobModel>
          columns={getColumns({
            t,
            dateLocale,
            onEdit: openEdit,
            onDelete: handleDelete,
            onRun: handleRun,
            onToggleEnabled: handleToggleEnabled,
            disableActions: isPending,
          })}
          dataSource={jobs}
          loading={isLoading}
          rowKey="id"
          scroll={{ x: "max-content" }}
          expandable={{
            expandedRowRender,
            rowExpandable: (record) =>
              Boolean(
                record.lastOutput || record.lastError || record.disabledReason,
              ),
          }}
          pagination={false}
        />
      </Flex>

      <CronFormDrawer
        open={drawerOpen}
        onClose={closeDrawer}
        onSubmit={handleSubmit}
        loading={creating || updating}
        job={jobToEdit}
        containers={containers ?? []}
        functions={functionNames}
        initialFunction={initialFunction}
      />
    </>
  );
}
