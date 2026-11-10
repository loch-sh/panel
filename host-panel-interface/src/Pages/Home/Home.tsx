import { Button, Card, Flex, List, Tag, Typography } from "antd";
import { ContainerInfo } from "dockerode";
import { useMemo } from "react";
import CountUpModule from "react-countup";
import { useTranslation } from "react-i18next";
import { Link } from "react-router";
import StackStatusTag from "../../Components/Base/StackStatusTag";
import ResourceOverview from "./ResourceOverview";
import useContainers from "../../Services/Containers/Hooks/useContainers";
import useDomains from "../../Services/Domains/Hooks/useDomains";
import useMe from "../../Services/Users/Hooks/useMe";
import { UserProfile } from "../../Services/Users/Models/users.model";
import { appConfig } from "../../config";
import { useContainerLabels } from "../../i18n/useContainerLabels";
import {
  displayContainerName,
  displayStackName,
} from "../../Utils/hostedStack";

const { Title, Text } = Typography;

// react-countup ships CJS only: since Vite 8 the default import resolves to the
// whole `module.exports` object ({ default, useCountUp }) instead of the component.
const CountUp =
  (CountUpModule as unknown as { default?: typeof CountUpModule }).default ??
  CountUpModule;

const PREVIEW_LIMIT = 5;

const { supportPhone, supportUrl, statusUrl } = appConfig;

interface StackPreview {
  name: string;
  runningCount: number;
  healthyCount: number;
  totalCount: number;
  hasErrorExits: boolean;
  latestCreated: number;
}

function buildStackPreviews(containers: ContainerInfo[]): StackPreview[] {
  const groups = new Map<string, ContainerInfo[]>();
  for (const c of containers) {
    const stack = c.Labels["com.docker.compose.project"] ?? "";
    if (!stack) continue;
    if (!groups.has(stack)) groups.set(stack, []);
    groups.get(stack)!.push(c);
  }

  return Array.from(groups.entries())
    .map(([name, items]) => ({
      name,
      runningCount: items.filter((c) => c.State === "running").length,
      healthyCount: items.filter(
        (c) =>
          c.State === "running" ||
          (c.State === "exited" && c.Status.includes("(0)")),
      ).length,
      totalCount: items.length,
      hasErrorExits: items.some(
        (c) => c.State === "exited" && !c.Status.includes("(0)"),
      ),
      latestCreated: Math.max(...items.map((c) => c.Created)),
    }))
    .sort((a, b) => b.latestCreated - a.latestCreated);
}

interface DomainPreview {
  domain: string;
  containerName: string;
}

export default function Home() {
  const { t } = useTranslation(["home", "common"]);
  const { statusLabel } = useContainerLabels();
  const { data } = useContainers();
  const { data: me } = useMe();
  const isAdmin = me?.profile === UserProfile.ADMIN;
  const { data: domains } = useDomains(isAdmin ?? false);

  const stacks = useMemo(() => (data ? buildStackPreviews(data) : []), [data]);

  const recentDomains = useMemo(() => {
    if (!domains || stacks.length === 0) return [];
    const stackOrder = new Map(stacks.map((s, i) => [s.name, i]));
    const seen = new Set<string>();
    const result: DomainPreview[] = [];

    const sorted = [...domains].sort((a, b) => {
      const sa =
        stackOrder.get(a.containerName.split("-").slice(0, -1).join("-")) ??
        Infinity;
      const sb =
        stackOrder.get(b.containerName.split("-").slice(0, -1).join("-")) ??
        Infinity;
      return sa - sb;
    });

    for (const entry of sorted) {
      for (const router of entry.routers) {
        for (const domain of router.domains) {
          if (!seen.has(domain)) {
            seen.add(domain);
            result.push({ domain, containerName: entry.containerName });
            if (result.length >= PREVIEW_LIMIT) return result;
          }
        }
      }
    }
    return result;
  }, [domains, stacks]);

  const errorContainers = useMemo(() => {
    if (!data) return [];
    return data.filter(
      (c) => c.State === "exited" && !c.Status.includes("(0)"),
    );
  }, [data]);

  const seeMoreButton = (to: string, target?: string) => (
    <Link to={to} target={target}>
      <Button type="link" size="small">
        {t("actions.seeMore", { ns: "common" })}
      </Button>
    </Link>
  );

  return (
    <Flex vertical gap="middle">
      <div>
        <Title level={4} style={{ margin: 0 }}>
          {t("title")}
        </Title>
      </div>

      {me && (
        <>
          {/* --- Ligne 1 : compteur, profil, domaines, erreurs --- */}
          <Flex gap="middle" className="home-row">
            <Flex vertical gap="middle">
              <Card
                style={{ textAlign: "center", flex: 1 }}
                title={t("cards.containerCount.title")}
              >
                {data && (
                  <CountUp
                    end={data.length}
                    duration={2}
                    style={{ fontSize: 50 }}
                  />
                )}
              </Card>
              <Card
                style={{ textAlign: "center", flex: 1 }}
                title={
                  isAdmin
                    ? t("cards.profile.adminTitle")
                    : t("cards.profile.userTitle")
                }
              >
                {isAdmin && <p>{t("cards.profile.adminAccess")}</p>}

                {me.profile === UserProfile.RESTRICTED &&
                  me.permissions.length === 0 && (
                    <p>{t("cards.profile.noAccess")}</p>
                  )}

                {me.profile === UserProfile.RESTRICTED &&
                  me.permissions.length > 0 && (
                    <p>
                      {t("cards.profile.restrictedAccess", {
                        containers: me.permissions.join(", "),
                      })}
                    </p>
                  )}
              </Card>
            </Flex>

            {/* Dernières stacks */}
            <Card
              title={t("cards.latestStacks.title")}
              extra={seeMoreButton("/containers")}
              style={{ flex: 1 }}
            >
              <List
                size="small"
                dataSource={stacks.slice(0, PREVIEW_LIMIT)}
                locale={{ emptyText: t("cards.latestStacks.empty") }}
                renderItem={(item) => (
                  <List.Item>
                    <Link
                      to={`/containers?search=${encodeURIComponent(displayStackName(item.name))}`}
                      style={{ fontWeight: 600 }}
                    >
                      {displayStackName(item.name)}
                    </Link>
                    <StackStatusTag
                      runningCount={item.runningCount}
                      healthyCount={item.healthyCount}
                      totalCount={item.totalCount}
                      style={{ marginLeft: "auto" }}
                    />
                    {item.hasErrorExits && (
                      <span
                        style={{ cursor: "help" }}
                        title={t("cards.latestStacks.abnormalExit")}
                      >
                        ⚠️
                      </span>
                    )}
                  </List.Item>
                )}
              />
            </Card>
          </Flex>

          {/* --- Ligne 2 : domaines + erreurs à gauche, statut à droite --- */}
          <Flex gap="middle" align="stretch" className="home-row">
            <Flex vertical gap="middle" style={{ flex: 1, minWidth: 0 }}>
              <ResourceOverview isAdmin={isAdmin} />

              {/* Domaines (admin) */}
              {isAdmin && (
                <Card
                  title={t("cards.latestDomains.title")}
                  extra={seeMoreButton("/domains")}
                  style={{ flex: 1 }}
                >
                  <List
                    size="small"
                    dataSource={recentDomains}
                    locale={{ emptyText: t("cards.latestDomains.empty") }}
                    renderItem={(item) => (
                      <List.Item
                        style={{
                          display: "grid",
                          gridTemplateColumns: "1fr 1fr",
                          gap: 12,
                        }}
                      >
                        <a
                          href={`https://${item.domain}`}
                          target="_blank"
                          rel="noreferrer"
                          style={{ wordBreak: "break-all" }}
                        >
                          {item.domain}
                        </a>
                        <Text
                          type="secondary"
                          style={{
                            fontSize: 12,
                            textAlign: "right",
                            wordBreak: "break-all",
                          }}
                        >
                          {displayContainerName(item.containerName)}
                        </Text>
                      </List.Item>
                    )}
                  />
                </Card>
              )}

              {/* Conteneurs en arrêt incorrect */}
              <Card
                title={t("cards.errorContainers.title")}
                extra={seeMoreButton("/containers")}
                style={{ flex: 1 }}
              >
                <List
                  size="small"
                  dataSource={errorContainers.slice(0, PREVIEW_LIMIT)}
                  locale={{ emptyText: t("cards.errorContainers.empty") }}
                  renderItem={(item) => (
                    <List.Item>
                      <Link
                        to={`/containers?search=${encodeURIComponent(displayContainerName(item.Names[0]?.replace(/^\//, "") ?? item.Id.slice(0, 12)))}`}
                        style={{ fontWeight: 600 }}
                      >
                        {displayContainerName(
                          item.Names[0]?.replace(/^\//, "") ??
                            item.Id.slice(0, 12),
                        )}
                      </Link>
                      <Tag color="red" style={{ marginLeft: "auto" }}>
                        {statusLabel(item.Status)}
                      </Tag>
                    </List.Item>
                  )}
                />
              </Card>

              {isAdmin && (supportPhone || supportUrl) && (
                <Card title={t("cards.support.title")}>
                  {supportPhone && (
                    <p>
                      <Link to={`tel:${supportPhone}`}>
                        {t("cards.support.emergency", { phone: supportPhone })}
                      </Link>
                    </p>
                  )}
                  {supportUrl && (
                    <p>
                      <Link to={supportUrl} target="_blank">
                        {t("cards.support.openTicket")}
                      </Link>
                    </p>
                  )}
                </Card>
              )}
            </Flex>

            {!!statusUrl && (
              <Card
                title={t("cards.serviceStatus.title")}
                extra={seeMoreButton(statusUrl, "_blank")}
                style={{
                  flex: 2,
                  minWidth: 0,
                  display: "flex",
                  flexDirection: "column",
                }}
                styles={{ body: { flex: 1, padding: 0 } }}
              >
                <iframe
                  src={statusUrl}
                  sandbox="allow-scripts allow-same-origin"
                  referrerPolicy="no-referrer"
                  loading="lazy"
                  style={{
                    width: "100%",
                    height: "100%",
                    border: "none",
                  }}
                  title={t("cards.serviceStatus.title")}
                />
              </Card>
            )}
          </Flex>
        </>
      )}
    </Flex>
  );
}
