import {
  Alert,
  Card,
  Collapse,
  Descriptions,
  Flex,
  Tag,
  Typography,
} from "antd";
import { differenceInSeconds, parseISO } from "date-fns";
import { useTranslation } from "react-i18next";
import {
  HostingOperation,
  OperationState,
} from "../../../Services/Hosting/Models/hosting.model";
import {
  formatApiDateToString,
  formatApiDateToTimeString,
} from "../../../Utils/date";
import type enErrors from "../../../i18n/locales/en/errors.json";

type ErrorCode = keyof typeof enErrors.codes;

/** Placeholder for a missing value (not text: no translation needed). */
const NO_VALUE = "—";

const STATE_COLORS = {
  running: "processing",
  succeeded: "success",
  failed: "error",
} as const satisfies Record<OperationState, string>;

function formatDateTime(date: string): string {
  return `${formatApiDateToString(date)} ${formatApiDateToTimeString(date)}`;
}

export default function OperationTab({
  operation,
}: Readonly<{ operation?: HostingOperation | null }>) {
  const { t } = useTranslation("hosting");
  const { t: tErrors, i18n } = useTranslation("errors");
  /** Translated message of an operation error code (generic fallback). */
  const errorMessage = (code?: string) =>
    code && i18n.exists(`errors:codes.${code}`)
      ? tErrors(`codes.${code as ErrorCode}`)
      : tErrors("fallback");

  if (!operation) {
    return (
      <Typography.Text type="secondary">{t("operation.none")}</Typography.Text>
    );
  }

  const duration = operation.finishedAt
    ? differenceInSeconds(
        parseISO(operation.finishedAt),
        parseISO(operation.startedAt),
      )
    : undefined;

  return (
    <Card size="small">
      <Flex vertical gap="middle">
        {operation.state === "failed" && (
          <Alert
            type="error"
            showIcon
            title={t("operation.failedAlert")}
            description={
              <>
                {errorMessage(operation.error)}
                {operation.rolledBack && <div>{t("operation.rolledBack")}</div>}
              </>
            }
          />
        )}

        <Descriptions
          bordered
          size="small"
          column={{ xs: 1, md: 2 }}
          items={[
            {
              key: "kind",
              label: t("operation.fields.kind"),
              children: t(`operation.kind.${operation.kind}`),
            },
            {
              key: "state",
              label: t("operation.fields.state"),
              children: (
                <Tag color={STATE_COLORS[operation.state]}>
                  {t(`operation.state.${operation.state}`)}
                </Tag>
              ),
            },
            {
              key: "startedAt",
              label: t("operation.fields.startedAt"),
              children: formatDateTime(operation.startedAt),
            },
            {
              key: "finishedAt",
              label: t("operation.fields.finishedAt"),
              children: operation.finishedAt
                ? formatDateTime(operation.finishedAt)
                : NO_VALUE,
            },
            ...(duration !== undefined
              ? [
                  {
                    key: "duration",
                    label: t("operation.fields.duration"),
                    children: t("operation.durationSeconds", {
                      seconds: duration,
                    }),
                  },
                ]
              : []),
          ]}
        />

        <Collapse
          defaultActiveKey={operation.state === "failed" ? ["output"] : []}
          items={[
            {
              key: "output",
              label: t("operation.output"),
              children: operation.output ? (
                <pre
                  style={{
                    margin: 0,
                    maxHeight: 400,
                    overflow: "auto",
                    fontFamily: "monospace",
                    fontSize: 12,
                    whiteSpace: "pre-wrap",
                    wordBreak: "break-all",
                  }}
                >
                  {operation.output}
                </pre>
              ) : (
                <Typography.Text type="secondary">
                  {t("operation.noOutput")}
                </Typography.Text>
              ),
            },
          ]}
        />
      </Flex>
    </Card>
  );
}
