import {
  AppstoreOutlined,
  AuditOutlined,
  ApartmentOutlined,
  ClockCircleOutlined,
  CodeOutlined,
  CloudServerOutlined,
  DatabaseOutlined,
  DockerOutlined,
  FunctionOutlined,
  GlobalOutlined,
  HomeOutlined,
  KeyOutlined,
  LogoutOutlined,
  InboxOutlined,
  MessageOutlined,
  SafetyCertificateOutlined,
  SettingOutlined,
  ThunderboltOutlined,
  UserOutlined,
} from "@ant-design/icons";
import { Avatar, Flex, Menu, theme } from "antd";
import type { TFunction } from "i18next";
import { useTranslation } from "react-i18next";
import {
  KeyboardEvent,
  MouseEvent,
  ReactInstance,
  ReactNode,
  useCallback,
  useMemo,
  useState,
} from "react";
import { Link, useLocation, useNavigate } from "react-router";
import { ItemType, MenuItemType } from "antd/es/menu/interface";
import useMe from "../../Services/Users/Hooks/useMe";
import { UserProfile } from "../../Services/Users/Models/users.model";
import useSftpConfig from "../../Services/Sftp/Hooks/useSftpConfig";
import useHostingConfig from "../../Services/Hosting/Hooks/useHostingConfig";
import useServicesConfig from "../../Services/Databases/Hooks/useServicesConfig";
import useFunctionsConfig from "../../Services/Functions/Hooks/useFunctionsConfig";
import {
  SERVICE_ENGINE_IDS,
  SERVICE_ENGINES,
  ServiceEngineId,
  ServiceKind,
} from "../../Services/Databases/Models/databases.model";

import { appConfig } from "../../config";
import LanguageSwitcher from "./LanguageSwitcher";

const keyToRoute = {
  home: "/",
  containers: "/containers",
  hosting: "/hosting",
  cron: "/cron",
  functions: "/functions",
  domains: "/domains",
  networks: "/networks",
  sftp: "/sftp",
  "svc-mysql": "/services/mysql",
  "svc-mariadb": "/services/mariadb",
  "svc-postgres": "/services/postgres",
  "svc-mongodb": "/services/mongodb",
  "svc-redis": "/services/redis",
  "svc-valkey": "/services/valkey",
  "svc-rabbitmq": "/services/rabbitmq",
  "svc-nats": "/services/nats",
  "svc-elasticmq": "/services/elasticmq",
  "svc-mosquitto": "/services/mosquitto",
  "svc-s3": "/services/s3",
  users: "/users",
  "api-keys": "/api-keys",
  audit: "/security/audit",
} as const satisfies Record<string, string>;

type RouteKey = keyof typeof keyToRoute;

const GROUP_KEYS = [
  "grp-compute",
  "grp-serverless",
  "grp-hosting",
  "grp-databases",
  "grp-cache",
  "grp-messaging",
  "grp-queue",
  "grp-storage",
  "grp-network",
  "grp-admin",
  "grp-security",
] as const;

type GroupKey = (typeof GROUP_KEYS)[number];

const keyToGroup: Partial<Record<RouteKey, GroupKey>> = {
  containers: "grp-compute",
  hosting: "grp-hosting",
  cron: "grp-serverless",
  functions: "grp-serverless",
  domains: "grp-network",
  networks: "grp-network",
  sftp: "grp-hosting",
  "svc-mysql": "grp-databases",
  "svc-mariadb": "grp-databases",
  "svc-postgres": "grp-databases",
  "svc-mongodb": "grp-databases",
  "svc-redis": "grp-cache",
  "svc-valkey": "grp-cache",
  "svc-rabbitmq": "grp-messaging",
  "svc-nats": "grp-messaging",
  "svc-elasticmq": "grp-queue",
  "svc-mosquitto": "grp-queue",
  "svc-s3": "grp-storage",
  users: "grp-admin",
  "api-keys": "grp-admin",
  audit: "grp-security",
};

const OPEN_KEYS_STORAGE_KEY = "menuOpenKeys";

interface MenuEntry {
  key: RouteKey;
  label: string;
  icon: ReactNode;
}

interface MenuGroup {
  key: GroupKey;
  label: string;
  icon: ReactNode;
  children: (MenuEntry | false)[];
  /** Keep the submenu even with a single visible entry. */
  alwaysGroup?: boolean;
}

interface MenuVisibility {
  isAdmin: boolean;
  sftpEnabled: boolean;
  hostingEnabled: boolean;
  functionsEnabled: boolean;
  /** Service engines enabled on the server. */
  serviceEngines: readonly ServiceEngineId[];
}

/**
 * Renders a group as a collapsible submenu. Empty groups are dropped and a
 * group with a single visible entry is flattened into a plain item, so the
 * menu never shows a chevron that hides only one link.
 */
function toMenuItem(group: MenuGroup): ItemType<MenuItemType> | null {
  const visible = group.children.filter((c): c is MenuEntry => c !== false);
  if (visible.length === 0) return null;
  if (visible.length === 1 && !group.alwaysGroup) {
    const [{ key, label, icon }] = visible;
    const item: MenuItemType = { key, label, icon };
    return item;
  }

  return {
    key: group.key,
    label: group.label,
    icon: group.icon,
    // Sub-items have no icon, like a cloud console sidebar.
    children: visible.map(({ key, label }) => ({ key, label })),
  };
}

/** One menu group per service category. */
const SERVICE_GROUPS: { kind: ServiceKind; key: GroupKey; icon: ReactNode }[] =
  [
    { kind: "database", key: "grp-databases", icon: <DatabaseOutlined /> },
    { kind: "cache", key: "grp-cache", icon: <ThunderboltOutlined /> },
    { kind: "messaging", key: "grp-messaging", icon: <MessageOutlined /> },
    { kind: "queue", key: "grp-queue", icon: <InboxOutlined /> },
    { kind: "storage", key: "grp-storage", icon: <CloudServerOutlined /> },
  ];

function getMenuItems(
  t: TFunction<"layout">,
  {
    isAdmin,
    sftpEnabled,
    hostingEnabled,
    functionsEnabled,
    serviceEngines,
  }: MenuVisibility,
): ItemType<MenuItemType>[] {
  const groups: MenuGroup[] = [
    {
      key: "grp-compute",
      label: t("menu.groups.compute"),
      icon: <AppstoreOutlined />,
      children: [
        {
          key: "containers",
          label: t("menu.containers"),
          icon: <DockerOutlined />,
        },
      ],
    },
    {
      key: "grp-serverless",
      label: t("menu.groups.serverless"),
      icon: <FunctionOutlined />,
      alwaysGroup: true,
      children: [
        { key: "cron", label: t("menu.cron"), icon: <ClockCircleOutlined /> },
        isAdmin &&
          functionsEnabled && {
            key: "functions",
            label: t("menu.functions"),
            icon: <CodeOutlined />,
          },
      ],
    },
    {
      key: "grp-hosting",
      label: t("menu.groups.hosting"),
      icon: <CloudServerOutlined />,
      children: [
        isAdmin &&
          hostingEnabled && {
            key: "hosting",
            label: t("menu.hostingSites"),
            icon: <GlobalOutlined />,
          },
        isAdmin &&
          sftpEnabled && {
            key: "sftp",
            label: t("menu.sftpShort"),
            icon: <CloudServerOutlined />,
          },
      ],
    },
    ...SERVICE_GROUPS.map(({ kind, key, icon }): MenuGroup => ({
      key,
      label: t(`menu.groups.${kind}`),
      icon,
      alwaysGroup: true,
      children: SERVICE_ENGINE_IDS.filter(
        (engine) => SERVICE_ENGINES[engine] === kind,
      ).map(
        (engine) =>
          isAdmin &&
          serviceEngines.includes(engine) && {
            key: `svc-${engine}` as const,
            label: t(`menu.services.${engine}`),
            icon,
          },
      ),
    })),
    {
      key: "grp-network",
      label: t("menu.groups.network"),
      icon: <GlobalOutlined />,
      children: [
        isAdmin && {
          key: "domains",
          label: t("menu.domains"),
          icon: <GlobalOutlined />,
        },
        isAdmin && {
          key: "networks",
          label: t("menu.networks"),
          icon: <ApartmentOutlined />,
        },
      ],
    },
    {
      key: "grp-admin",
      label: t("menu.groups.admin"),
      icon: <SettingOutlined />,
      children: [
        isAdmin && {
          key: "users",
          label: t("menu.users"),
          icon: <UserOutlined />,
        },
        { key: "api-keys", label: t("menu.apiKeys"), icon: <KeyOutlined /> },
      ],
    },
    {
      key: "grp-security",
      label: t("menu.groups.security"),
      icon: <SafetyCertificateOutlined />,
      alwaysGroup: true,
      children: [
        isAdmin && {
          key: "audit",
          label: t("menu.auditTrail"),
          icon: <AuditOutlined />,
        },
      ],
    },
  ];

  return [
    { key: "home", label: t("menu.home"), icon: <HomeOutlined /> },
    ...groups
      .map(toMenuItem)
      .filter((item): item is ItemType<MenuItemType> => item !== null),
    { key: "logout", label: t("menu.logout"), icon: <LogoutOutlined /> },
  ];
}

/** Menu key of the current route: exact match for "/", longest prefix otherwise. */
function findActiveKey(pathname: string): RouteKey | undefined {
  let best: RouteKey | undefined;
  let bestLength = -1;

  for (const [key, route] of Object.entries(keyToRoute) as [
    RouteKey,
    string,
  ][]) {
    const matches =
      route === "/"
        ? pathname === "/"
        : pathname === route || pathname.startsWith(`${route}/`);
    if (matches && route.length > bestLength) {
      best = key;
      bestLength = route.length;
    }
  }

  return best;
}

function isGroupKey(value: unknown): value is GroupKey {
  return (GROUP_KEYS as readonly unknown[]).includes(value);
}

function readStoredOpenKeys(): GroupKey[] {
  try {
    const parsed: unknown = JSON.parse(
      localStorage.getItem(OPEN_KEYS_STORAGE_KEY) ?? "[]",
    );
    return Array.isArray(parsed) ? parsed.filter(isGroupKey) : [];
  } catch {
    return [];
  }
}

function storeOpenKeys(keys: string[]) {
  try {
    localStorage.setItem(OPEN_KEYS_STORAGE_KEY, JSON.stringify(keys));
  } catch {
    // Storage unavailable (private mode, blocked site data): not critical.
  }
}

function withGroup(keys: string[], group?: GroupKey): string[] {
  return group && !keys.includes(group) ? [...keys, group] : keys;
}

interface MenuInfo {
  key: string;
  keyPath: string[];
  /** @deprecated This will not support in future. You should avoid to use this */
  item: ReactInstance;
  domEvent: MouseEvent<HTMLElement> | KeyboardEvent<HTMLElement>;
}

interface Props {
  collapsed: boolean;
  onNavigate?: () => void;
}

export default function MenuLayout({ collapsed, onNavigate }: Readonly<Props>) {
  const navigate = useNavigate();
  const location = useLocation();
  const { data: me } = useMe();
  // Admin-only endpoints: not called for restricted users (guaranteed 403).
  const { data: sftpConfig } = useSftpConfig(me?.profile === UserProfile.ADMIN);
  const { data: hostingConfig } = useHostingConfig(
    me?.profile === UserProfile.ADMIN,
  );
  const { data: servicesConfig } = useServicesConfig(
    me?.profile === UserProfile.ADMIN,
  );
  const { data: functionsConfig } = useFunctionsConfig(
    me?.profile === UserProfile.ADMIN,
  );
  const { token } = theme.useToken();
  const { t } = useTranslation("layout");

  const activeKey = findActiveKey(location.pathname);
  const activeGroup = activeKey ? keyToGroup[activeKey] : undefined;

  // Groups opened by the user (remembered), plus the group of the current
  // route, which is opened whenever navigation lands in it.
  const [openKeys, setOpenKeys] = useState<string[]>(() =>
    withGroup(readStoredOpenKeys(), activeGroup),
  );
  const [prevActiveGroup, setPrevActiveGroup] = useState(activeGroup);
  if (activeGroup !== prevActiveGroup) {
    setPrevActiveGroup(activeGroup);
    setOpenKeys((keys) => withGroup(keys, activeGroup));
  }

  const onOpenChange = useCallback((keys: string[]) => {
    setOpenKeys(keys);
    storeOpenKeys(keys);
  }, []);

  const isAdmin = me?.profile === UserProfile.ADMIN;
  const sftpEnabled = sftpConfig?.enabled ?? false;
  const hostingEnabled = hostingConfig?.enabled ?? false;
  const functionsEnabled = functionsConfig?.enabled ?? false;
  const serviceEngines = useMemo(
    () =>
      servicesConfig?.enabled
        ? SERVICE_ENGINE_IDS.filter((e) =>
            servicesConfig.engines?.some((x) => x.id === e),
          )
        : [],
    [servicesConfig],
  );
  const items = useMemo(
    () =>
      getMenuItems(t, {
        isAdmin,
        sftpEnabled,
        hostingEnabled,
        functionsEnabled,
        serviceEngines,
      }),
    [t, isAdmin, sftpEnabled, hostingEnabled, functionsEnabled, serviceEngines],
  );

  const onClick = useCallback(
    (info: MenuInfo) => {
      if (info.key === "logout") {
        window.location.replace(`${appConfig.apiUrl}/openid-connect/logout`);
        return;
      }

      if (info.key in keyToRoute) {
        navigate(keyToRoute[info.key as RouteKey]);
        onNavigate?.();
      }
    },
    [navigate, onNavigate],
  );

  const initials = useMemo(() => {
    if (!me) return "";

    const split = me.name.split(" ");

    if (split.length > 1) {
      return split[0].substring(0, 1) + split[1].substring(0, 1);
    }

    return split[0].substring(0, 1);
  }, [me]);

  return (
    <Flex vertical style={{ height: "100%" }}>
      <div
        style={{
          padding: "16px 0",
          textAlign: "center",
          borderBottom: `1px solid ${token.colorBorderSecondary}`,
        }}
      >
        <Link to="/" onClick={() => onNavigate?.()}>
          <img
            src={appConfig.logoUrl}
            alt={appConfig.appName}
            width={collapsed ? 40 : 80}
          />
        </Link>
      </div>

      <Flex
        justify="center"
        align="center"
        vertical
        gap="small"
        style={{ padding: "12px 0" }}
      >
        <Avatar
          style={{
            backgroundColor: token.colorPrimary,
            verticalAlign: "middle",
          }}
          size="default"
        >
          {initials}
        </Avatar>
        {!collapsed && (
          <span style={{ color: token.colorText, fontSize: 13 }}>
            {me?.name}
          </span>
        )}
      </Flex>

      <Menu
        mode="inline"
        selectedKeys={activeKey ? [activeKey] : []}
        // When the sider is collapsed, groups become hover popups managed by
        // antd: controlling openKeys there would leave popups stuck open.
        {...(collapsed ? {} : { openKeys, onOpenChange })}
        onClick={onClick}
        items={items}
        style={{ flex: 1, borderRight: 0 }}
      />

      <LanguageSwitcher collapsed={collapsed} />
    </Flex>
  );
}
