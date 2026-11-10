import { Col, Row } from "antd";
import { useTranslation } from "react-i18next";
import { HostingLimits } from "../../../Services/Hosting/Models/hosting.model";
import ResourceLimitField from "./ResourceLimitField";

const MIN_MEMORY_MB = 64;
const MIN_CPUS = 0.1;
const MEMORY_STEP_MB = 64;
const CPU_STEP = 0.1;
/** Values proposed when a limit is switched on (clamped to the server limits). */
const DEFAULT_MEMORY_LIMIT_MB = 512;
const DEFAULT_CPU_LIMIT = 1;

/** Optional memory and CPU limits of the container. */
export default function ResourcesFields({
  limits,
}: Readonly<{ limits: Pick<HostingLimits, "maxMemoryMB" | "maxCpus"> }>) {
  const { t } = useTranslation("hosting");
  return (
    <Row gutter={24}>
      <Col xs={24} md={12}>
        <ResourceLimitField
          field="memoryMB"
          label={t("form.fields.memory.label")}
          toggleLabel={t("form.fields.memory.toggle")}
          help={t("form.fields.memory.help")}
          unit={t("form.fields.memory.unit")}
          min={MIN_MEMORY_MB}
          max={limits.maxMemoryMB}
          step={MEMORY_STEP_MB}
          precision={0}
          initialLimit={DEFAULT_MEMORY_LIMIT_MB}
          requiredMessage={t("form.required")}
        />
      </Col>
      <Col xs={24} md={12}>
        <ResourceLimitField
          field="cpus"
          label={t("form.fields.cpus.label")}
          toggleLabel={t("form.fields.cpus.toggle")}
          help={t("form.fields.cpus.help")}
          unit={t("form.fields.cpus.unit")}
          min={MIN_CPUS}
          max={limits.maxCpus}
          step={CPU_STEP}
          precision={1}
          initialLimit={DEFAULT_CPU_LIMIT}
          requiredMessage={t("form.required")}
        />
      </Col>
    </Row>
  );
}
