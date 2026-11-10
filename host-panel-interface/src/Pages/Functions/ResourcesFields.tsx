import { Alert, Col, Form, InputNumber, Row, Typography } from "antd";
import { useTranslation } from "react-i18next";
import {
  FunctionLimits,
  FunctionsInfoModel,
  MAX_CPUS,
  MAX_TMP_MB,
  MIN_CPUS,
  MIN_TMP_MB,
} from "../../Services/Functions/Models/functions.model";
import { formatMB } from "./functionLabels";

/** Why the limits are refused (null: acceptable). */
export function limitsProblem(
  l: FunctionLimits,
  config: FunctionsInfoModel,
): keyof FunctionLimits | null {
  const valid = (v: number, min: number, max: number) =>
    Number.isFinite(v) && v >= min && v <= max;
  if (
    !Number.isInteger(l.memoryMB) ||
    !valid(l.memoryMB, config.minMemoryMB, config.maxMemoryMB)
  ) {
    return "memoryMB";
  }
  if (
    !valid(l.cpus, MIN_CPUS, MAX_CPUS) ||
    Math.abs(l.cpus * 100 - Math.round(l.cpus * 100)) > 1e-6
  ) {
    return "cpus";
  }
  if (
    !Number.isInteger(l.timeoutSeconds) ||
    !valid(l.timeoutSeconds, 1, config.maxTimeoutSeconds)
  ) {
    return "timeoutSeconds";
  }
  if (
    !Number.isInteger(l.tmpSizeMB) ||
    !valid(l.tmpSizeMB, MIN_TMP_MB, MAX_TMP_MB)
  ) {
    return "tmpSizeMB";
  }
  return null;
}

interface Props {
  value: FunctionLimits;
  onChange: (value: FunctionLimits) => void;
  config: FunctionsInfoModel;
  disabled?: boolean;
  /** the function's current limits (edition): not counted twice in the budget */
  current?: FunctionLimits;
}

/** Memory, CPU, timeout and /tmp size, within the configuration's bounds. */
export default function ResourcesFields({
  value,
  onChange,
  config,
  disabled = false,
  current,
}: Readonly<Props>) {
  const { t } = useTranslation("functions");
  const set = (key: keyof FunctionLimits) => (v: number | null) =>
    onChange({ ...value, [key]: v ?? Number.NaN });
  const problem = limitsProblem(value, config);
  const freeMemory =
    config.maxTotalMemoryMB > 0
      ? config.maxTotalMemoryMB - config.usedMemoryMB + (current?.memoryMB ?? 0)
      : null;
  const freeCpus =
    config.maxTotalCpus > 0
      ? Math.round(
          (config.maxTotalCpus - config.usedCpus + (current?.cpus ?? 0)) * 100,
        ) / 100
      : null;
  const overBudget =
    (freeMemory !== null && value.memoryMB > freeMemory) ||
    (freeCpus !== null && value.cpus > freeCpus);
  const status = (key: keyof FunctionLimits) =>
    problem === key ? ("error" as const) : undefined;

  return (
    <Form layout="vertical" disabled={disabled} component="div">
      <Row gutter={16}>
        <Col xs={24} md={12}>
          <Form.Item
            label={t("resources.memory")}
            htmlFor="fn-memory"
            validateStatus={status("memoryMB")}
            help={t("resources.memoryHelp", {
              min: formatMB(config.minMemoryMB),
              max: formatMB(config.maxMemoryMB),
            })}
          >
            <InputNumber
              id="fn-memory"
              value={value.memoryMB}
              min={config.minMemoryMB}
              max={config.maxMemoryMB}
              step={64}
              precision={0}
              suffix={t("units.mb")}
              onChange={set("memoryMB")}
              style={{ width: "100%" }}
            />
          </Form.Item>
        </Col>
        <Col xs={24} md={12}>
          <Form.Item
            label={t("resources.cpus")}
            htmlFor="fn-cpus"
            validateStatus={status("cpus")}
            help={t("resources.cpusHelp", { min: MIN_CPUS, max: MAX_CPUS })}
          >
            <InputNumber
              id="fn-cpus"
              value={value.cpus}
              min={MIN_CPUS}
              max={MAX_CPUS}
              step={0.1}
              precision={2}
              onChange={set("cpus")}
              style={{ width: "100%" }}
            />
          </Form.Item>
        </Col>
        <Col xs={24} md={12}>
          <Form.Item
            label={t("resources.timeout")}
            htmlFor="fn-timeout"
            validateStatus={status("timeoutSeconds")}
            help={t("resources.timeoutHelp", { max: config.maxTimeoutSeconds })}
          >
            <InputNumber
              id="fn-timeout"
              value={value.timeoutSeconds}
              min={1}
              max={config.maxTimeoutSeconds}
              precision={0}
              suffix={t("units.s")}
              onChange={set("timeoutSeconds")}
              style={{ width: "100%" }}
            />
          </Form.Item>
        </Col>
        <Col xs={24} md={12}>
          <Form.Item
            label={t("resources.tmp")}
            htmlFor="fn-tmp"
            validateStatus={status("tmpSizeMB")}
            help={t("resources.tmpHelp", {
              min: formatMB(MIN_TMP_MB),
              max: formatMB(MAX_TMP_MB),
            })}
          >
            <InputNumber
              id="fn-tmp"
              value={value.tmpSizeMB}
              min={MIN_TMP_MB}
              max={MAX_TMP_MB}
              step={64}
              precision={0}
              suffix={t("units.mb")}
              onChange={set("tmpSizeMB")}
              style={{ width: "100%" }}
            />
          </Form.Item>
        </Col>
      </Row>
      <Typography.Paragraph type="secondary" style={{ marginBottom: 8 }}>
        {t("resources.tmpNote")}
      </Typography.Paragraph>
      {(freeMemory !== null || freeCpus !== null) && (
        <Alert
          type={overBudget ? "warning" : "info"}
          showIcon
          title={
            overBudget
              ? t("resources.overBudget")
              : t("resources.budget", {
                  memory:
                    freeMemory !== null
                      ? formatMB(Math.max(0, freeMemory))
                      : "∞",
                  cpus: freeCpus !== null ? Math.max(0, freeCpus) : "∞",
                })
          }
        />
      )}
    </Form>
  );
}
