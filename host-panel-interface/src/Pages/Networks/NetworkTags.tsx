import { WarningOutlined } from "@ant-design/icons";
import { Badge, Space, Tag, Tooltip, Typography } from "antd";
import { useTranslation } from "react-i18next";
import { Link } from "react-router";
import {
  DockerNetworkOrigin,
  networkProblemKey,
  NetworkState,
  SYSTEM_NETWORK,
} from "../../Services/Networks/Models/networks.model";
import { networkPath } from "./networkPaths";

/** Shared (system network of the services module) or Private. */
export function NetworkScopeTag({ system }: Readonly<{ system?: boolean }>) {
  const { t } = useTranslation("networks");
  const key = system ? "shared" : "private";
  return (
    <Tooltip title={t(`tagHints.${key}`)}>
      <Tag color={system ? "geekblue" : "purple"}>{t(`tags.${key}`)}</Tag>
    </Tooltip>
  );
}

/** Every network the panel creates is internal. */
export function NoInternetTag() {
  const { t } = useTranslation("networks");
  return (
    <Tooltip title={t("tagHints.noInternet")}>
      <Tag>{t("tags.noInternet")}</Tag>
    </Tooltip>
  );
}

const STATE_STATUS = {
  ok: "success",
  missing: "warning",
  compromised: "error",
} as const;

/** State of the Docker network, with the problem in a tooltip. */
export function NetworkStateBadge({
  state,
  problem,
}: Readonly<{ state: NetworkState; problem?: string }>) {
  const { t } = useTranslation("networks");
  const hint =
    state === "compromised"
      ? `${t("stateHints.compromised")} ${t(`problems.${networkProblemKey(problem)}`)}`
      : state === "missing" && problem
        ? `${t("stateHints.missing")} ${t(`problems.${networkProblemKey(problem)}`)}`
        : t(`stateHints.${state}`);
  return (
    <Tooltip title={hint}>
      <Badge
        status={STATE_STATUS[state] ?? "default"}
        text={t(`state.${state}`)}
      />
    </Tooltip>
  );
}

/** Network names as tags linking to their page. */
export function NetworkLinks({
  networks,
}: Readonly<{ networks: readonly string[] | undefined }>) {
  if (!networks || networks.length === 0) {
    return <Typography.Text type="secondary">—</Typography.Text>;
  }
  return (
    <Space size={4} wrap>
      {networks.map((n) => (
        <Tag key={n} color={n === SYSTEM_NETWORK ? "geekblue" : "purple"}>
          <Link to={networkPath(n)}>{n}</Link>
        </Tag>
      ))}
    </Space>
  );
}

/** The subnet overlaps a reserved range (LAN / VPN used outside Docker). */
export function ReservedOverlapTag({
  range,
}: Readonly<{ range: string | undefined }>) {
  const { t } = useTranslation("networks");
  if (!range) return null;
  return (
    <Tooltip title={t("tagHints.reservedOverlap", { range })}>
      <Tag color="orange" icon={<WarningOutlined />}>
        {t("tags.reservedOverlap")}
      </Tag>
    </Tooltip>
  );
}

const ORIGIN_COLORS: Record<DockerNetworkOrigin, string> = {
  docker: "default",
  compose: "blue",
  other: "gold",
};

/** Who created a Docker network the panel does not manage. */
export function DockerOriginTag({
  origin,
  composeProject,
}: Readonly<{ origin: DockerNetworkOrigin; composeProject?: string }>) {
  const { t } = useTranslation("networks");
  return (
    <Tooltip title={t(`docker.originHints.${origin}`)}>
      <Tag color={ORIGIN_COLORS[origin] ?? "default"}>
        {origin === "compose" && composeProject
          ? t("docker.origins.composeProject", { project: composeProject })
          : t(`docker.origins.${origin}`)}
      </Tag>
    </Tooltip>
  );
}
