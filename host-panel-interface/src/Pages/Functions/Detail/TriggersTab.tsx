import { PlusOutlined } from "@ant-design/icons";
import { Button, Card, Flex, Table, Tag, Typography } from "antd";
import { useMemo } from "react";
import { useTranslation } from "react-i18next";
import { useNavigate } from "react-router";
import useCronJobs from "../../../Services/Cron/Hooks/useCronJobs";
import {
  CronJobModel,
  CronJobType,
  MAX_JOBS_PER_FUNCTION,
} from "../../../Services/Cron/Models/cron.model";
import { FunctionDetailModel } from "../../../Services/Functions/Models/functions.model";
import { formatDateTime } from "../functionLabels";

const { Text } = Typography;

/** Path of the cron page, opening the creation of an invocation of `name`. */
export const newInvocationPath = (name: string) =>
  `/cron?newFunction=${encodeURIComponent(name)}`;

/** Scheduled tasks (cron) invoking this function. */
export default function TriggersTab({
  fn,
}: Readonly<{ fn: FunctionDetailModel }>) {
  const { t, i18n } = useTranslation("functions");
  const { t: tc } = useTranslation("cron");
  const lang = i18n.resolvedLanguage;
  const navigate = useNavigate();
  const { data: jobs, isLoading } = useCronJobs();
  const own = useMemo(
    () =>
      (jobs ?? []).filter(
        (j) =>
          j.type === CronJobType.FUNCTION_INVOKE &&
          j.functionInvoke?.function === fn.name,
      ),
    [jobs, fn.name],
  );
  const full = own.length >= MAX_JOBS_PER_FUNCTION;

  return (
    <Card
      title={t("triggers.title")}
      extra={
        <Button
          icon={<PlusOutlined />}
          disabled={full}
          onClick={() => navigate(newInvocationPath(fn.name))}
        >
          {t("triggers.create")}
        </Button>
      }
    >
      <Flex vertical gap="middle">
        <Text type="secondary">
          {t("triggers.intro", { max: MAX_JOBS_PER_FUNCTION })}
        </Text>
        <Table<CronJobModel>
          dataSource={own}
          loading={isLoading}
          rowKey="id"
          pagination={false}
          scroll={{ x: "max-content" }}
          locale={{ emptyText: t("triggers.empty") }}
          columns={[
            { title: tc("table.columns.name"), dataIndex: "name" },
            {
              title: tc("table.columns.schedule"),
              dataIndex: "schedule",
              render: (s: string) => <Text code>{s}</Text>,
            },
            {
              title: t("triggers.event"),
              key: "event",
              render: (_: unknown, j) => (
                <Text
                  code
                  ellipsis={{ tooltip: j.functionInvoke?.event }}
                  style={{ maxWidth: 240 }}
                >
                  {j.functionInvoke?.event ?? "{}"}
                </Text>
              ),
            },
            {
              title: tc("table.columns.mode"),
              key: "mode",
              render: (_: unknown, j) =>
                !j.enabled ? (
                  <Tag>{tc("modes.disabled")}</Tag>
                ) : j.once ? (
                  <Tag color="orange">{tc("modes.once")}</Tag>
                ) : (
                  <Tag color="green">{tc("modes.recurring")}</Tag>
                ),
            },
            {
              title: tc("table.columns.lastStatus"),
              dataIndex: "lastStatus",
              render: (s: CronJobModel["lastStatus"]) => tc(`statuses.${s}`),
            },
            {
              title: tc("table.columns.lastRun"),
              dataIndex: "lastRun",
              render: (v?: string) => formatDateTime(v, lang),
            },
          ]}
        />
        <Button
          type="link"
          onClick={() => navigate("/cron")}
          style={{ alignSelf: "flex-start", padding: 0 }}
        >
          {t("triggers.manage")}
        </Button>
      </Flex>
    </Card>
  );
}
