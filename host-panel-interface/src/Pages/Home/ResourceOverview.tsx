import {
  ApartmentOutlined,
  AppstoreOutlined,
  ClockCircleOutlined,
  CloudServerOutlined,
  CodeOutlined,
  DatabaseOutlined,
  DockerOutlined,
  GlobalOutlined,
  InboxOutlined,
  KeyOutlined,
  LinkOutlined,
  MessageOutlined,
  ThunderboltOutlined,
} from "@ant-design/icons";
import { Card, Grid, Spin, theme } from "antd";
import { ReactNode, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { Link } from "react-router";
import useApiKeys from "../../Services/ApiKeys/Hooks/useApiKeys";
import useContainers from "../../Services/Containers/Hooks/useContainers";
import useCronJobs from "../../Services/Cron/Hooks/useCronJobs";
import useServiceInstances from "../../Services/Databases/Hooks/useServiceInstances";
import useServicesConfig from "../../Services/Databases/Hooks/useServicesConfig";
import {
  SERVICE_ENGINE_IDS,
  SERVICE_ENGINES,
  SERVICE_KINDS,
  ServiceEngineModel,
  ServiceInstanceModel,
  ServiceKind,
} from "../../Services/Databases/Models/databases.model";
import useDomains from "../../Services/Domains/Hooks/useDomains";
import { DomainEntry } from "../../Services/Domains/Models/domains.model";
import useFunctions from "../../Services/Functions/Hooks/useFunctions";
import useFunctionsConfig from "../../Services/Functions/Hooks/useFunctionsConfig";
import useHostingConfig from "../../Services/Hosting/Hooks/useHostingConfig";
import useHostingStacks from "../../Services/Hosting/Hooks/useHostingStacks";
import useNetworks from "../../Services/Networks/Hooks/useNetworks";
import useSftpConfig from "../../Services/Sftp/Hooks/useSftpConfig";
import useSftpUsers from "../../Services/Sftp/Hooks/useSftpUsers";

/** Count of a tile: a number, still loading, or the query failed. */
type TileCount = number | "loading" | "error";

interface QueryLike<T> {
  data: T | undefined;
  isError: boolean;
}

/** Keeps the last data on a failed refetch; "error" only without any data. */
function countOf<T>(
  query: QueryLike<T>,
  count: (data: T) => number,
): TileCount {
  if (query.data !== undefined) return count(query.data);
  return query.isError ? "error" : "loading";
}

const length = (items: readonly unknown[]) => items.length;

/** Distinct compose projects among the containers the user can see. */
function countStacks(containers: { Labels: Record<string, string> }[]) {
  return new Set(
    containers
      .map((c) => c.Labels["com.docker.compose.project"] ?? "")
      .filter(Boolean),
  ).size;
}

/** Distinct domain names routed to the containers. */
function countDomains(entries: DomainEntry[]) {
  return new Set(entries.flatMap((e) => e.routers.flatMap((r) => r.domains)))
    .size;
}

/**
 * User-facing resources of the instances of the given engines: the resources
 * of a shared instance (databases, vhosts), one per dedicated instance.
 */
function countServiceResources(
  instances: ServiceInstanceModel[],
  engines: ServiceEngineModel[],
) {
  const byId = new Map(engines.map((e) => [e.id, e]));
  return instances.reduce((sum, instance) => {
    const engine = byId.get(instance.engine);
    if (!engine) return sum;
    return (
      sum + (engine.lifecycle === "dedicated" ? 1 : (instance.resources ?? 0))
    );
  }, 0);
}

/** Icons of the service categories, as in the menu. */
const KIND_ICONS: Record<ServiceKind, ReactNode> = {
  database: <DatabaseOutlined />,
  cache: <ThunderboltOutlined />,
  messaging: <MessageOutlined />,
  queue: <InboxOutlined />,
  storage: <CloudServerOutlined />,
};

interface TileModel {
  key: string;
  label: string;
  icon: ReactNode;
  to: string;
  count: TileCount;
}

interface Props {
  isAdmin: boolean;
}

/**
 * "Resource overview" card of the home page: one tile per resource type the
 * user can see (same gates as the menu), with its count, linking to its page.
 */
export default function ResourceOverview({ isAdmin }: Readonly<Props>) {
  const { t } = useTranslation("home");
  const screens = Grid.useBreakpoint();

  // Module configurations: admin-only endpoints are not called otherwise.
  const { data: sftpConfig } = useSftpConfig(isAdmin);
  const { data: hostingConfig } = useHostingConfig(isAdmin);
  const { data: servicesConfig } = useServicesConfig(isAdmin);
  const { data: functionsConfig } = useFunctionsConfig(isAdmin);

  const sftpEnabled = isAdmin && (sftpConfig?.enabled ?? false);
  const hostingEnabled = isAdmin && (hostingConfig?.enabled ?? false);
  const functionsEnabled = isAdmin && (functionsConfig?.enabled ?? false);
  const servicesEnabled = isAdmin && (servicesConfig?.enabled ?? false);

  const containers = useContainers();
  const cron = useCronJobs();
  const apiKeys = useApiKeys();
  const hostingStacks = useHostingStacks(hostingEnabled);
  const instances = useServiceInstances(servicesEnabled);
  const functions = useFunctions(functionsEnabled);
  const networks = useNetworks(isAdmin);
  const sftpUsers = useSftpUsers(sftpEnabled);
  const domains = useDomains(isAdmin);

  // Enabled engines of each category, in menu order.
  const enginesByKind = useMemo(() => {
    const result = new Map<ServiceKind, ServiceEngineModel[]>();
    if (!servicesEnabled) return result;
    for (const id of SERVICE_ENGINE_IDS) {
      const engine = servicesConfig?.engines?.find((e) => e.id === id);
      if (!engine) continue;
      const kind = SERVICE_ENGINES[id];
      result.set(kind, [...(result.get(kind) ?? []), engine]);
    }
    return result;
  }, [servicesEnabled, servicesConfig]);

  const tiles: TileModel[] = [
    {
      key: "containers",
      label: t("cards.resourceOverview.resources.containers"),
      icon: <DockerOutlined />,
      to: "/containers",
      count: countOf(containers, length),
    },
    {
      key: "stacks",
      label: t("cards.resourceOverview.resources.stacks"),
      icon: <AppstoreOutlined />,
      to: "/containers",
      count: countOf(containers, countStacks),
    },
  ];
  if (hostingEnabled) {
    tiles.push({
      key: "hosting",
      label: t("cards.resourceOverview.resources.hosting"),
      icon: <GlobalOutlined />,
      to: "/hosting",
      count: countOf(hostingStacks, length),
    });
  }
  for (const kind of SERVICE_KINDS) {
    const engines = enginesByKind.get(kind);
    if (!engines?.length) continue;
    tiles.push({
      key: `services-${kind}`,
      label: t(`cards.resourceOverview.resources.${kind}`),
      icon: KIND_ICONS[kind],
      to: `/services/${engines[0].id}`,
      count: countOf(instances, (data) => countServiceResources(data, engines)),
    });
  }
  if (functionsEnabled) {
    tiles.push({
      key: "functions",
      label: t("cards.resourceOverview.resources.functions"),
      icon: <CodeOutlined />,
      to: "/functions",
      count: countOf(functions, length),
    });
  }
  tiles.push({
    key: "cron",
    label: t("cards.resourceOverview.resources.cron"),
    icon: <ClockCircleOutlined />,
    to: "/cron",
    count: countOf(cron, length),
  });
  if (isAdmin) {
    tiles.push({
      key: "networks",
      label: t("cards.resourceOverview.resources.networks"),
      icon: <ApartmentOutlined />,
      to: "/networks",
      count: countOf(networks, length),
    });
  }
  if (sftpEnabled) {
    tiles.push({
      key: "sftp",
      label: t("cards.resourceOverview.resources.sftp"),
      icon: <CloudServerOutlined />,
      to: "/sftp",
      count: countOf(sftpUsers, length),
    });
  }
  if (isAdmin) {
    tiles.push({
      key: "domains",
      label: t("cards.resourceOverview.resources.domains"),
      icon: <LinkOutlined />,
      to: "/domains",
      count: countOf(domains, countDomains),
    });
  }
  tiles.push({
    key: "apiKeys",
    label: t("cards.resourceOverview.resources.apiKeys"),
    icon: <KeyOutlined />,
    to: "/api-keys",
    count: countOf(apiKeys, length),
  });

  return (
    <Card title={t("cards.resourceOverview.title")}>
      <div
        role="list"
        style={{
          display: "grid",
          gridTemplateColumns: screens.md ? "repeat(2, minmax(0, 1fr))" : "1fr",
          // Every row as tall as the tallest one: tiles keep equal heights.
          gridAutoRows: "1fr",
          gap: 12,
        }}
      >
        {tiles.map(({ key, ...tile }) => (
          <div role="listitem" key={key} style={{ display: "flex" }}>
            <ResourceTile {...tile} />
          </div>
        ))}
      </div>
    </Card>
  );
}

function ResourceTile({ label, icon, to, count }: Omit<TileModel, "key">) {
  const { t } = useTranslation("home");
  const { token } = theme.useToken();
  const [hovered, setHovered] = useState(false);

  let value: string;
  if (count === "loading") value = t("cards.resourceOverview.loading");
  else if (count === "error") value = t("cards.resourceOverview.unavailable");
  else value = String(count);

  return (
    <Link
      to={to}
      aria-label={t("cards.resourceOverview.tileLabel", { label, value })}
      onMouseEnter={() => setHovered(true)}
      onMouseLeave={() => setHovered(false)}
      style={{
        flex: 1,
        display: "flex",
        alignItems: "center",
        gap: 12,
        padding: "12px 16px",
        borderRadius: token.borderRadiusLG,
        background: hovered
          ? token.colorFillSecondary
          : token.colorFillTertiary,
        color: token.colorText,
        transition: `background ${token.motionDurationMid}`,
      }}
    >
      <span
        aria-hidden
        style={{
          flex: "none",
          display: "inline-flex",
          alignItems: "center",
          justifyContent: "center",
          width: 32,
          height: 32,
          borderRadius: token.borderRadius,
          background: token.colorPrimaryBg,
          color: token.colorPrimary,
          fontSize: 16,
        }}
      >
        {icon}
      </span>
      <span
        style={{
          flex: 1,
          minWidth: 0,
          fontWeight: 500,
          overflowWrap: "anywhere",
        }}
      >
        {label}
      </span>
      <span
        aria-hidden
        title={count === "error" ? value : undefined}
        style={{
          flex: "none",
          minWidth: 32,
          padding: "0 10px",
          lineHeight: "24px",
          textAlign: "center",
          borderRadius: 999,
          background: token.colorBgContainer,
          border: `1px solid ${token.colorBorderSecondary}`,
          fontWeight: 600,
          fontVariantNumeric: "tabular-nums",
        }}
      >
        {count === "loading" ? (
          <Spin size="small" />
        ) : count === "error" ? (
          "—"
        ) : (
          count
        )}
      </span>
    </Link>
  );
}
