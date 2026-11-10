import { Flex, Segmented, Select, Skeleton, Switch } from "antd";
import Search from "antd/es/input/Search";
import Fuse from "fuse.js";
import { useEffect, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { PanelTitle } from "../../Components/Base/PanelTitle";
import useDomains from "../../Services/Domains/Hooks/useDomains";
import { DomainEntry } from "../../Services/Domains/Models/domains.model";
import { useLanguage } from "../../i18n/useLocales";
import DomainsGraph from "./DomainsGraph";
import DomainsTable from "./DomainsTable";
import { displayContainerName } from "../../Utils/hostedStack";

/** Entry with display names: the "hosted-" prefix of hosted stacks is hidden. */
function toDisplayEntry(entry: DomainEntry): DomainEntry {
  return {
    ...entry,
    containerName: displayContainerName(entry.containerName),
    routers: entry.routers.map((router) => ({
      ...router,
      name: displayContainerName(router.name),
      serviceName: displayContainerName(router.serviceName),
    })),
    services: (entry.services ?? []).map((service) => ({
      ...service,
      name: displayContainerName(service.name),
    })),
  };
}

type ViewMode = "table" | "graph";

export default function Domains() {
  const { t } = useTranslation("domains");
  const language = useLanguage();
  const { data: rawData, isLoading } = useDomains();
  // Names shown without the internal "hosted-" prefix (search included).
  const data = useMemo(() => rawData?.map(toDisplayEntry), [rawData]);
  const [view, setView] = useState<ViewMode>("table");
  const [searchInput, setSearchInput] = useState("");
  const [search, setSearch] = useState("");
  const [hideExited, setHideExited] = useState(false);
  const [selectedTld, setSelectedTld] = useState<string | null>(null);

  const tldOptions = useMemo(() => {
    if (!data) return [];
    const tlds = new Set<string>();
    for (const entry of data) {
      for (const router of entry.routers) {
        for (const domain of router.domains) {
          const parts = domain.split(".");
          if (parts.length >= 2) {
            tlds.add(`.${parts.slice(-2).join(".")}`);
          }
        }
      }
    }
    return Array.from(tlds)
      .sort()
      .map((tld) => ({ label: tld, value: tld }));
  }, [data]);

  useEffect(() => {
    const timer = setTimeout(() => setSearch(searchInput), 300);
    return () => clearTimeout(timer);
  }, [searchInput]);

  const fuse = useMemo(
    () =>
      new Fuse<DomainEntry>(data ?? [], {
        keys: [
          "containerName",
          "routers.domains",
          "routers.name",
          "routers.middlewares",
        ],
        threshold: 0.35,
        ignoreLocation: true,
      }),
    [data],
  );

  const filtered = useMemo(() => {
    if (!data) return [];
    let result = search ? fuse.search(search).map((r) => r.item) : data;
    if (hideExited) {
      result = result.filter((e) => e.containerState !== "exited");
    }
    if (selectedTld) {
      result = result.filter((e) =>
        e.routers.some((r) =>
          r.domains.some((d) => d.endsWith(selectedTld.slice(1))),
        ),
      );
    }
    return result;
  }, [data, search, fuse, hideExited, selectedTld]);

  if (isLoading) {
    return <Skeleton />;
  }

  return (
    <>
      <div style={{ marginBottom: 20 }}>
        <PanelTitle>{t("title")}</PanelTitle>
      </div>

      <Flex gap="middle" align="center" className="toolbar" style={{ paddingBottom: 15 }}>
        <Search
          lang={language}
          allowClear
          placeholder={t("toolbar.searchPlaceholder")}
          onChange={(ev) => setSearchInput(ev.target.value)}
          style={{ width: 300 }}
        />
        <Select
          allowClear
          placeholder={t("toolbar.tldPlaceholder")}
          options={tldOptions}
          value={selectedTld}
          onChange={(v) => setSelectedTld(v ?? null)}
          style={{ width: 200 }}
        />
        <Switch
          checked={hideExited}
          onChange={setHideExited}
          checkedChildren={t("toolbar.activeOnly")}
          unCheckedChildren={t("toolbar.all")}
        />
        <Segmented
          options={[
            { label: t("toolbar.views.table"), value: "table" },
            { label: t("toolbar.views.graph"), value: "graph" },
          ]}
          value={view}
          onChange={(v) => setView(v as ViewMode)}
        />
      </Flex>

      {view === "table" ? (
        <DomainsTable data={filtered} />
      ) : (
        <DomainsGraph data={filtered} />
      )}
    </>
  );
}
