import { Flex, Segmented, Skeleton, Switch, Table, Tag, Tooltip } from "antd";
import StackStatusTag from "../../Components/Base/StackStatusTag";
import { ColumnsType } from "antd/es/table";
import { ContainerInfo } from "dockerode";
import {
  useCallback,
  useEffect,
  useMemo,
  useState,
  type ReactNode,
} from "react";
import { Link, useSearchParams } from "react-router";
import { toast } from "react-toastify";
import type { TFunction } from "i18next";
import { useTranslation } from "react-i18next";
import { PanelTitle } from "../../Components/Base/PanelTitle";
import ContainerSidePanel from "../../Components/ContainerSidePanel/ContainerSidePanel";
import useContainerAction from "../../Services/Containers/Hooks/useContainerAction";
import useContainers from "../../Services/Containers/Hooks/useContainers";
import useStackAction from "../../Services/Containers/Hooks/useStackAction";
import {
  ContainerActionEnum,
  StackActionEnum,
} from "../../Services/Containers/Models/containers.model";
import { getApiErrorMessage } from "../../Utils/apiErrors";
import StackActions from "./StackActions";
import Search from "antd/es/input/Search";
import ExpandedContainerTable from "./ExpandedContainerTable";
import StackStatsToggle from "../../Components/StackStats/StackStatsToggle";
import {
  displayContainerName,
  displayStackName,
  isHostedContainer,
} from "../../Utils/hostedStack";
import { serviceEngineOf } from "../../Utils/serviceStack";
import {
  filterStackType,
  isStackTypeFilter,
  STACK_TYPE_FILTERS,
  type StackTypeFilter,
} from "../../Utils/stackType";
import type { ServiceEngineId } from "../../Services/Databases/Models/databases.model";

interface StackGroup {
  /** real compose project (used for the API), or STANDALONE_KEY */
  key: string;
  /** name shown to the user ("hosted-" prefix stripped) */
  stackName: string;
  /** stack managed by the hosting feature */
  hosted: boolean;
  /** engine of a stack of the services module (database, cache...) */
  serviceEngine: ServiceEngineId | null;
  containers: ContainerInfo[];
  runningCount: number;
  healthyCount: number;
  totalCount: number;
  hasErrorExits: boolean;
}

const STACKS_PAGE_SIZE = 20;

const STANDALONE_KEY = "__standalone__";

const STACK_SUCCESS_KEYS = {
  [StackActionEnum.START]: "stackActions.started",
  [StackActionEnum.STOP]: "stackActions.stopped",
  [StackActionEnum.RESTART]: "stackActions.restarted",
} as const;

/**
 * Exact, case-insensitive search on container names, image and Compose stack.
 * Every whitespace-separated term must appear (e.g. "api prod").
 */
function filterContainers(
  containers: ContainerInfo[],
  search: string,
): ContainerInfo[] {
  const terms = search.toLowerCase().split(/\s+/).filter(Boolean);
  if (terms.length === 0) return containers;

  return containers.filter((c) => {
    // Search on displayed names, so the hidden "hosted-" prefix never matches.
    const haystack = [
      ...c.Names.map((n) => displayContainerName(n.replace(/^\//, ""))),
      c.Image,
      displayStackName(c.Labels["com.docker.compose.project"] ?? ""),
    ]
      .join(" ")
      .toLowerCase();
    return terms.every((term) => haystack.includes(term));
  });
}

function getStackColumns(
  t: TFunction<"containers">,
  onToggle: (key: string) => void,
  renderActions: (record: StackGroup) => ReactNode,
): ColumnsType<StackGroup> {
  return [
    {
      title: t("columns.stack"),
      dataIndex: "stackName",
      render: (value, record) => (
        <Flex gap={8} align="center">
          <span
            style={{ fontWeight: 600, cursor: "pointer" }}
            onClick={() => onToggle(record.key)}
          >
            {value}
          </span>
          {record.hosted && (
            <Link to={`/hosting/${encodeURIComponent(record.stackName)}`}>
              <Tag color="blue" style={{ cursor: "pointer" }}>
                {t("list.hostingTag")}
              </Tag>
            </Link>
          )}
          {record.serviceEngine && (
            <Link to={`/services/${record.serviceEngine}`}>
              <Tag color="purple" style={{ cursor: "pointer" }}>
                {t("list.serviceTag")}
              </Tag>
            </Link>
          )}
        </Flex>
      ),
    },
    {
      title: t("columns.containers"),
      render: (_, record) => (
        <>
          <StackStatusTag
            runningCount={record.runningCount}
            healthyCount={record.healthyCount}
            totalCount={record.totalCount}
          />
          {record.hasErrorExits && (
            <Tooltip title={t("list.errorExitsTooltip")}>
              <span className="error-pulse" style={{ cursor: "help" }}>
                ⚠️
              </span>
            </Tooltip>
          )}
        </>
      ),
    },
    {
      title: t("columns.actions"),
      align: "right",
      render: (_, record) => renderActions(record),
    },
  ];
}

function groupByStack(
  containers: ContainerInfo[],
  noStackLabel: string,
): StackGroup[] {
  const groups = new Map<string, ContainerInfo[]>();
  for (const container of containers) {
    const stack = container.Labels["com.docker.compose.project"] ?? "";
    if (!groups.has(stack)) groups.set(stack, []);
    groups.get(stack)!.push(container);
  }

  return Array.from(groups.entries())
    .map(([stackName, items]): StackGroup => ({
      key: stackName || STANDALONE_KEY,
      stackName: stackName ? displayStackName(stackName) : noStackLabel,
      hosted: items.some((c) => isHostedContainer(c.Labels)),
      serviceEngine:
        items.map((c) => serviceEngineOf(c.Labels)).find(Boolean) ?? null,
      containers: items,
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
    }))
    .sort((a, b) => {
      if (a.key === STANDALONE_KEY) return -1;
      if (b.key === STANDALONE_KEY) return 1;
      return a.stackName.localeCompare(b.stackName);
    });
}

export default function Containers() {
  const { t } = useTranslation("containers");
  const [searchParams, setSearchParams] = useSearchParams();
  const initialSearch = searchParams.get("search") ?? "";
  // ?stack=<compose project>: show only that stack, expanded (links from the
  // hosting pages). Exact match: display names may collide (hidden prefix).
  const [stackFilter, setStackFilter] = useState(
    searchParams.get("stack") ?? "",
  );

  // ?type=hosting|services|other: kind of stack shown.
  const [typeFilter, setTypeFilter] = useState<StackTypeFilter>(() => {
    const type = searchParams.get("type");
    return isStackTypeFilter(type) ? type : "all";
  });

  const [drawerOpen, setDrawerOpen] = useState("");
  const [searchInput, setSearchInput] = useState(initialSearch);
  const [search, setSearch] = useState(initialSearch);
  const [expandedStacks, setExpandedStacks] = useState<string[]>(() =>
    stackFilter ? [stackFilter] : [],
  );
  const [hideInactive, setHideInactive] = useState(false);
  const [showErrorsOnly, setShowErrorsOnly] = useState(false);
  const [currentPage, setCurrentPage] = useState(1);

  const { isLoading, data } = useContainers();
  const { mutateAsync: doAction, isPending: isContainerPending } =
    useContainerAction();
  const {
    mutate: doStackAction,
    isPending: isStackPending,
    variables: stackVariables,
  } = useStackAction();
  const isPending = isContainerPending || isStackPending;

  const handleAction = useCallback(
    (id: string, action: ContainerActionEnum) => doAction({ id, action }),
    [doAction],
  );

  const toggleStack = useCallback(
    (key: string) =>
      setExpandedStacks((prev) =>
        prev.includes(key) ? prev.filter((k) => k !== key) : [...prev, key],
      ),
    [],
  );

  const handleStackAction = useCallback(
    (stack: string, action: StackActionEnum) => {
      const name = displayStackName(stack);
      doStackAction(
        { stack, action },
        {
          onSuccess: ({ failed, results }) => {
            if (failed > 0) {
              toast.warning(
                t("stackActions.partialFailure", {
                  name,
                  failed,
                  total: results.length,
                }),
              );
            } else {
              toast.success(t(STACK_SUCCESS_KEYS[action], { name }));
            }
          },
          onError: (err) => toast.error(getApiErrorMessage(err)),
        },
      );
    },
    [doStackAction, t],
  );

  const renderStackActions = useCallback(
    (record: StackGroup) =>
      record.key === STANDALONE_KEY ? null : (
        <StackActions
          stack={record.key}
          displayName={record.stackName}
          runningCount={record.runningCount}
          totalCount={record.totalCount}
          disabled={isPending}
          loading={isStackPending && stackVariables?.stack === record.key}
          onAction={handleStackAction}
        />
      ),
    [handleStackAction, isPending, isStackPending, stackVariables],
  );

  const stackColumns = useMemo(
    () => getStackColumns(t, toggleStack, renderStackActions),
    [t, toggleStack, renderStackActions],
  );

  const handleExpand = useCallback((expanded: boolean, record: StackGroup) => {
    setExpandedStacks((prev) =>
      expanded ? [...prev, record.key] : prev.filter((k) => k !== record.key),
    );
  }, []);

  const expandedRowRender = useCallback(
    (record: StackGroup) => (
      <>
        <StackStatsToggle containers={record.containers} />
        <ExpandedContainerTable
          containers={record.containers}
          isTransiting={isPending}
          onAction={handleAction}
          onOpenDrawer={setDrawerOpen}
        />
      </>
    ),
    [isPending, handleAction],
  );

  // Debounce de la recherche (300ms)
  useEffect(() => {
    const timer = setTimeout(() => setSearch(searchInput), 300);
    return () => clearTimeout(timer);
  }, [searchInput]);

  // Nettoyer le query param après lecture initiale
  useEffect(() => {
    if (
      searchParams.has("search") ||
      searchParams.has("stack") ||
      searchParams.has("type")
    ) {
      setSearchParams({}, { replace: true });
    }
  }, []); // eslint-disable-line react-hooks/exhaustive-deps

  // Revenir en page 1 quand les filtres changent
  useEffect(() => {
    setCurrentPage(1);
  }, [search, hideInactive, showErrorsOnly, stackFilter, typeFilter]);

  const stackGroups = useMemo(() => {
    if (!data) return [];

    const filtered = filterContainers(data, search);

    let groups = groupByStack(filtered, t("list.noStack"));
    if (stackFilter) groups = groups.filter((g) => g.key === stackFilter);
    groups = filterStackType(groups, typeFilter);
    if (hideInactive) groups = groups.filter((g) => g.runningCount > 0);
    if (showErrorsOnly) groups = groups.filter((g) => g.hasErrorExits);
    return groups;
  }, [data, search, hideInactive, showErrorsOnly, stackFilter, typeFilter, t]);

  // Auto-expand toutes les stacks quand on arrive avec une recherche pré-remplie
  useEffect(() => {
    if (
      initialSearch &&
      stackGroups.length > 0 &&
      expandedStacks.length === 0
    ) {
      setExpandedStacks(stackGroups.map((g) => g.key));
    }
  }, [stackGroups]); // eslint-disable-line react-hooks/exhaustive-deps

  if (isLoading) {
    return <Skeleton />;
  }

  return (
    <>
      <style>{`
        @keyframes error-pulse {
          0%, 100% { opacity: 1; }
          50% { opacity: 0.2; }
        }
        .error-pulse {
          animation: error-pulse 1.5s ease-in-out infinite;
        }
      `}</style>
      <div style={{ marginBottom: 20 }}>
        <PanelTitle>{t("list.title")}</PanelTitle>
      </div>

      <Flex
        gap="middle"
        align="center"
        className="toolbar"
        style={{ paddingBottom: 15 }}
      >
        <Search
          allowClear
          placeholder={t("list.searchPlaceholder")}
          defaultValue={initialSearch}
          onChange={(ev) => setSearchInput(ev.target.value)}
          style={{ width: 300 }}
        />
        <Segmented<StackTypeFilter>
          aria-label={t("list.type.label")}
          value={typeFilter}
          onChange={setTypeFilter}
          options={STACK_TYPE_FILTERS.map((value) => ({
            value,
            label: t(`list.type.${value}`),
          }))}
        />
        <Switch
          checked={hideInactive}
          onChange={setHideInactive}
          checkedChildren={t("list.filterActive")}
          unCheckedChildren={t("list.filterAll")}
        />
        <Switch
          checked={showErrorsOnly}
          onChange={setShowErrorsOnly}
          checkedChildren={t("list.filterErrors")}
          unCheckedChildren={t("list.filterErrors")}
        />
        {stackFilter && (
          <Tag closable onClose={() => setStackFilter("")} color="blue">
            {t("list.stackFilter", { name: displayStackName(stackFilter) })}
          </Tag>
        )}
      </Flex>

      <Table<StackGroup>
        style={{ width: "100%" }}
        columns={stackColumns}
        loading={isLoading}
        dataSource={stackGroups}
        rowKey="key"
        pagination={
          stackGroups.length > STACKS_PAGE_SIZE
            ? {
                pageSize: STACKS_PAGE_SIZE,
                current: currentPage,
                onChange: setCurrentPage,
              }
            : false
        }
        expandable={{
          expandedRowKeys: expandedStacks,
          onExpand: handleExpand,
          expandedRowRender,
        }}
      />

      <ContainerSidePanel
        open={!!drawerOpen}
        id={drawerOpen}
        onClose={() => setDrawerOpen("")}
      />
    </>
  );
}
