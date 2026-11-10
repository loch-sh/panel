import { Badge, Space, Tag, Tooltip } from "antd";
import type { TFunction } from "i18next";
import { useTranslation } from "react-i18next";
import { formatBytes } from "../../Utils/bytes";
import {
  DeploymentModel,
  deploymentDate,
  FunctionState,
  isCustomRuntime,
  isDeploymentWarning,
  isFunctionState,
} from "../../Services/Functions/Models/functions.model";

export const FUNCTIONS_PATH = "/functions";
export const functionPath = (name: string, tab?: string) =>
  `${FUNCTIONS_PATH}/${encodeURIComponent(name)}${tab ? `?tab=${tab}` : ""}`;

const RUNTIMES = ["nodejs", "python", "provided"] as const;
type RuntimeId = (typeof RUNTIMES)[number];
const isRuntimeId = (id: string): id is RuntimeId =>
  (RUNTIMES as readonly string[]).includes(id);

/** "Node.js", "Python", "Go, Rust… (custom runtime)" (an unknown runtime id is shown as is). */
export function runtimeLabel(t: TFunction<"functions">, runtime: string) {
  return isRuntimeId(runtime) ? t(`runtimes.${runtime}`) : runtime;
}

/** Key of a runtime's description (Node.js for an unknown one). */
export const runtimeDescriptionKey = (runtime: string) =>
  `runtimeDescriptions.${isRuntimeId(runtime) ? runtime : "nodejs"}` as const;

/** Key of the handler help of a runtime (Node.js for an unknown one). */
export const handlerHelpKey = (runtime: string) =>
  `create.handlerHelp.${isRuntimeId(runtime) ? runtime : "nodejs"}` as const;

/** Translated warnings of a deployment (unknown codes are skipped). */
export function deploymentWarnings(
  t: TFunction<"functions">,
  d: Pick<DeploymentModel, "warnings">,
): string[] {
  return (d.warnings ?? [])
    .filter(isDeploymentWarning)
    .map((w) => t(`deploymentWarnings.${w}`));
}

/** "Node.js 22", "Go, Rust… (custom runtime, provided.al2023)" */
export function runtimeVersionLabel(
  t: TFunction<"functions">,
  runtime: string,
  version: string,
) {
  if (isCustomRuntime(runtime)) return t("runtimeCustomVersion", { version });
  return `${runtimeLabel(t, runtime)} ${version}`.trim();
}

const STATE_STATUS: Record<
  FunctionState,
  "success" | "default" | "warning" | "error" | "processing"
> = {
  not_deployed: "default",
  running: "success",
  stopped: "default",
  missing: "warning",
  modified: "error",
  unverified: "error",
  invalid: "error",
};

/** State of a function, with its meaning in a tooltip. */
export function FunctionStateBadge({
  state,
  health,
}: Readonly<{ state: string; health?: string }>) {
  const { t } = useTranslation("functions");
  if (!isFunctionState(state)) return <Badge status="default" text={state} />;
  const unhealthy = state === "running" && health === "unhealthy";
  const starting = state === "running" && health === "starting";
  return (
    <Space size={4} wrap>
      <Tooltip title={t(`stateHints.${state}`)}>
        <Badge
          status={
            unhealthy
              ? "warning"
              : starting
                ? "processing"
                : STATE_STATUS[state]
          }
          text={t(`states.${state}`)}
        />
      </Tooltip>
      {unhealthy && <Tag color="orange">{t("health.unhealthy")}</Tag>}
      {starting && <Tag color="blue">{t("health.starting")}</Tag>}
    </Space>
  );
}

/** Outbound Internet access of a function. */
export function EgressTag({ egress }: Readonly<{ egress: boolean }>) {
  const { t } = useTranslation("functions");
  return (
    <Tooltip title={egress ? t("egress.onHint") : t("egress.offHint")}>
      <Tag color={egress ? "orange" : "default"}>
        {egress ? t("egress.on") : t("egress.off")}
      </Tag>
    </Tooltip>
  );
}

/** A date and time in the UI language ("—" when unknown). */
export function formatDateTime(
  value: Date | string | null | undefined,
  lang: string | undefined,
): string {
  if (!value) return "—";
  const d = typeof value === "string" ? new Date(value) : value;
  // Go's zero time (a function whose reference is unreadable).
  if (Number.isNaN(d.getTime()) || d.getUTCFullYear() < 2000) return "—";
  return new Intl.DateTimeFormat(lang, {
    dateStyle: "medium",
    timeStyle: "short",
  }).format(d);
}

/** Date of a deployment, from its id. */
export function formatDeploymentDate(
  id: string | undefined,
  lang: string | undefined,
): string {
  return formatDateTime(deploymentDate(id), lang);
}

/** "512 MB", "1.5 GB" from a size in MB (units of the UI language). */
export function formatMB(mb: number): string {
  return formatBytes(mb * 1024 * 1024);
}
