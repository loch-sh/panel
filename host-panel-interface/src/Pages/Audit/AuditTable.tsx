import { Flex, Table, Tag, Tooltip, Typography } from "antd";
import { ColumnsType } from "antd/es/table";
import { memo, useMemo } from "react";
import { useTranslation } from "react-i18next";
import {
  AUDIT_EVENTS,
  AuditCategory,
  AuditEntryModel,
  AuditLevel,
  isAuditLevel,
  isKnownAuditEvent,
} from "../../Services/Audit/Models/audit.model";
import { auditEventLabel, auditLevelLabel } from "./auditLabels";

const { Text } = Typography;

const LEVEL_COLORS: Record<AuditLevel, string> = {
  INFO: "blue",
  WARN: "orange",
  ERROR: "red",
};

const CATEGORY_COLORS: Record<AuditCategory, string> = {
  auth: "geekblue",
  security: "volcano",
  containers: "cyan",
  cron: "purple",
  users: "gold",
  apikeys: "magenta",
  sftp: "lime",
  hosting: "green",
  services: "blue",
  networks: "purple",
  functions: "orange",
};

/** Detail tags shown in the row; the expanded row has everything. */
const MAX_DETAIL_TAGS = 6;

const preStyle = {
  margin: 0,
  padding: 8,
  fontSize: 12,
  maxHeight: 300,
  overflow: "auto",
  background: "var(--ant-color-fill-tertiary)",
  borderRadius: 4,
} as const;

function formatTime(format: Intl.DateTimeFormat, time: string): string {
  const d = new Date(time);
  return Number.isNaN(d.getTime()) ? time : format.format(d);
}

function formatValue(v: unknown): string {
  if (v === null || v === undefined) return "";
  if (typeof v === "object") return JSON.stringify(v);
  return String(v);
}

const rowKey = (e: AuditEntryModel) => e.id;

const expandable = {
  expandedRowRender: (e: AuditEntryModel) => (
    <pre style={preStyle}>
      {JSON.stringify(
        {
          time: e.time,
          level: e.level,
          event: e.event,
          user: e.user,
          ...e.fields,
        },
        null,
        2,
      )}
    </pre>
  ),
};

interface Props {
  entries: AuditEntryModel[];
  loading: boolean;
}

function AuditTable({ entries, loading }: Readonly<Props>) {
  const { t, i18n } = useTranslation("audit");

  const columns = useMemo<ColumnsType<AuditEntryModel>>(() => {
    const dateFormat = new Intl.DateTimeFormat(i18n.resolvedLanguage, {
      dateStyle: "short",
      timeStyle: "medium",
    });
    return [
      {
        title: t("table.time"),
        dataIndex: "time",
        width: 190,
        render: (time: string) => (
          <Text style={{ whiteSpace: "nowrap" }}>
            {formatTime(dateFormat, time)}
          </Text>
        ),
      },
      {
        title: t("table.level"),
        dataIndex: "level",
        width: 110,
        render: (level: string) => (
          <Tag color={isAuditLevel(level) ? LEVEL_COLORS[level] : undefined}>
            {auditLevelLabel(t, level)}
          </Tag>
        ),
      },
      {
        title: t("table.event"),
        dataIndex: "event",
        render: (event: string) => {
          const category = isKnownAuditEvent(event)
            ? AUDIT_EVENTS[event]
            : undefined;
          return (
            <Flex gap={4} wrap align="center">
              {category && (
                <Tag color={CATEGORY_COLORS[category]}>
                  {t(`categories.${category}`)}
                </Tag>
              )}
              <Tooltip title={event}>
                {/* Focusable: the raw event name is reachable by keyboard. */}
                <Text tabIndex={0}>{auditEventLabel(t, event)}</Text>
              </Tooltip>
            </Flex>
          );
        },
      },
      {
        title: t("table.user"),
        dataIndex: "user",
        render: (u?: string) =>
          u ? (
            <Text copyable={{ text: u }}>{u}</Text>
          ) : (
            <Text type="secondary">—</Text>
          ),
      },
      {
        title: t("table.details"),
        dataIndex: "fields",
        render: (fields?: Record<string, unknown>) =>
          fields ? (
            <Flex gap={4} wrap>
              {Object.entries(fields)
                .slice(0, MAX_DETAIL_TAGS)
                .map(([k, v]) => {
                  const value = formatValue(v);
                  return (
                    <Tag
                      key={k}
                      title={`${k}: ${value}`}
                      style={{
                        maxWidth: 320,
                        overflow: "hidden",
                        textOverflow: "ellipsis",
                        whiteSpace: "nowrap",
                      }}
                    >
                      <Text type="secondary">{k}:</Text> {value}
                    </Tag>
                  );
                })}
            </Flex>
          ) : null,
      },
    ];
  }, [t, i18n.resolvedLanguage]);

  const locale = useMemo(() => ({ emptyText: t("empty") }), [t]);

  return (
    <Table<AuditEntryModel>
      rowKey={rowKey}
      columns={columns}
      dataSource={entries}
      loading={loading}
      pagination={false}
      size="small"
      scroll={{ x: "max-content" }}
      locale={locale}
      expandable={expandable}
    />
  );
}

export default memo(AuditTable);
