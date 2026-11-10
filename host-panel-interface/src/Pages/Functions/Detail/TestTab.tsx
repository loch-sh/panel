import { CaretRightOutlined, FormatPainterOutlined } from "@ant-design/icons";
import {
  Alert,
  Button,
  Card,
  Descriptions,
  Flex,
  Form,
  Input,
  Tag,
  Typography,
  theme,
} from "antd";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { useFunctionInvoke } from "../../../Services/Functions/Hooks/useFunctionMutations";
import {
  FunctionDetailModel,
  FunctionsInfoModel,
  InvokeResultModel,
} from "../../../Services/Functions/Models/functions.model";
import { getApiErrorMessage } from "../../../Utils/apiErrors";
import { formatBytes } from "../../../Utils/bytes";
import {
  base64Size,
  displayPayload,
  eventProblem,
  formatEvent,
} from "../eventJson";

const { Text } = Typography;

/** Untrusted text written by the function, as text only (never HTML). */
function TextBlock({ text, label }: Readonly<{ text: string; label: string }>) {
  const { token } = theme.useToken();
  return (
    <pre
      aria-label={label}
      style={{
        margin: 0,
        padding: token.paddingSM,
        maxHeight: 360,
        overflow: "auto",
        fontFamily: token.fontFamilyCode,
        fontSize: token.fontSizeSM,
        whiteSpace: "pre-wrap",
        wordBreak: "break-all",
        background: token.colorFillTertiary,
        borderRadius: token.borderRadius,
      }}
    >
      {text}
    </pre>
  );
}

function InvokeResult({ result }: Readonly<{ result: InvokeResultModel }>) {
  const { t, i18n } = useTranslation("functions");
  const lang = i18n.resolvedLanguage;
  const ok = result.statusCode >= 200 && result.statusCode < 300;
  return (
    <Card title={t("test.result")}>
      <Flex vertical gap="middle">
        <Descriptions
          column={{ xs: 1, md: 3 }}
          size="small"
          items={[
            {
              key: "status",
              label: t("test.statusCode"),
              children: (
                <Tag color={ok && !result.functionError ? "green" : "red"}>
                  {result.statusCode}
                </Tag>
              ),
            },
            {
              key: "duration",
              label: t("test.duration"),
              children: t("test.durationValue", {
                value: result.durationMs.toLocaleString(lang),
              }),
            },
            {
              key: "request",
              label: t("test.requestId"),
              children: result.requestId ? (
                <Text code>{result.requestId}</Text>
              ) : (
                "—"
              ),
            },
          ]}
        />
        {result.functionError && (
          <Alert
            type="error"
            showIcon
            title={t("test.functionError", { error: result.functionError })}
          />
        )}
        <Flex vertical gap={4}>
          <Text strong>{t("test.payload")}</Text>
          {result.payload !== undefined ? (
            result.payload === "" ? (
              <Text type="secondary">{t("test.payloadEmpty")}</Text>
            ) : (
              <TextBlock
                label={t("test.payload")}
                text={displayPayload(result.payload)}
              />
            )
          ) : result.payloadBase64 ? (
            <>
              <Text type="secondary">
                {t("test.payloadBinary", {
                  size: formatBytes(base64Size(result.payloadBase64)),
                })}
              </Text>
              <TextBlock
                label={t("test.payload")}
                text={result.payloadBase64}
              />
            </>
          ) : (
            <Text type="secondary">{t("test.payloadEmpty")}</Text>
          )}
        </Flex>
        <Flex vertical gap={4}>
          <Text strong>{t("test.logs")}</Text>
          {result.logTail ? (
            <TextBlock label={t("test.logs")} text={result.logTail} />
          ) : (
            <Text type="secondary">{t("test.logsEmpty")}</Text>
          )}
        </Flex>
      </Flex>
    </Card>
  );
}

/** Invokes the function with a JSON event, shows the envelope as text. */
export default function TestTab({
  fn,
  config,
}: Readonly<{ fn: FunctionDetailModel; config: FunctionsInfoModel }>) {
  const { t } = useTranslation("functions");
  const [event, setEvent] = useState("{}");
  const [result, setResult] = useState<InvokeResultModel | null>(null);
  const [error, setError] = useState<string | null>(null);
  const { mutateAsync: invoke, isPending } = useFunctionInvoke();
  const problem = eventProblem(event, config.maxEventBytes);
  const runnable = fn.state === "running";

  const run = async () => {
    if (problem || isPending) return;
    setError(null);
    try {
      setResult(await invoke({ name: fn.name, event }));
    } catch (err) {
      setResult(null);
      setError(getApiErrorMessage(err));
    }
  };

  return (
    <Flex vertical gap="middle">
      <Card title={t("test.eventTitle")}>
        <Flex vertical gap="small">
          {!runnable && (
            <Alert type="info" showIcon title={t("test.notRunning")} />
          )}
          <Form layout="vertical" component="div">
            <Form.Item
              label={t("test.eventLabel")}
              htmlFor="fn-event"
              validateStatus={problem ? "error" : undefined}
              help={
                problem
                  ? t(`test.eventProblems.${problem}`, {
                      max: formatBytes(config.maxEventBytes),
                    })
                  : t("test.eventHelp")
              }
              style={{ marginBottom: 0 }}
            >
              <Input.TextArea
                id="fn-event"
                value={event}
                onChange={(e) => setEvent(e.target.value)}
                autoSize={{ minRows: 6, maxRows: 20 }}
                spellCheck={false}
                style={{ fontFamily: "monospace" }}
              />
            </Form.Item>
          </Form>
          <Flex gap="small" wrap>
            <Button
              type="primary"
              icon={<CaretRightOutlined />}
              loading={isPending}
              disabled={!!problem}
              onClick={() => void run()}
            >
              {t("test.invoke")}
            </Button>
            <Button
              icon={<FormatPainterOutlined />}
              disabled={problem === "empty" || problem === "invalid"}
              onClick={() => setEvent(formatEvent(event))}
            >
              {t("test.format")}
            </Button>
          </Flex>
        </Flex>
      </Card>
      {error && <Alert type="error" showIcon title={error} />}
      {result && <InvokeResult result={result} />}
    </Flex>
  );
}
