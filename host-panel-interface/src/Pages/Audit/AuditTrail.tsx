import { ReloadOutlined, SafetyCertificateOutlined } from "@ant-design/icons";
import { Alert, Button, Flex, Result, Spin, Switch, Typography } from "antd";
import { Dayjs } from "dayjs";
import { useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { PanelTitle } from "../../Components/Base/PanelTitle";
import useAuditEntries, {
  useAuditRefresh,
} from "../../Services/Audit/Hooks/useAuditEntries";
import {
  AuditFilters,
  AuditLevel,
} from "../../Services/Audit/Models/audit.model";
import useMe from "../../Services/Users/Hooks/useMe";
import { UserProfile } from "../../Services/Users/Models/users.model";
import { getApiErrorMessage } from "../../Utils/apiErrors";
import AuditFiltersBar, {
  AuditFilterValues,
  EMPTY_FILTERS,
} from "./AuditFiltersBar";
import AuditTable from "./AuditTable";

const { Text } = Typography;

/** The trail's own consultations, hidden unless asked for. */
const VIEWED_EVENT = "audit trail viewed";

/**
 * The end of the range is picked to the minute and the API bound is
 * exclusive: the whole selected minute is included.
 */
function rangeEnd(d: Dayjs | null): string | undefined {
  return d?.startOf("minute").add(1, "minute").toISOString();
}

/** Read-only view of the audit log, for admins. */
export default function AuditTrail() {
  const { t } = useTranslation("audit");
  const { data: me, isLoading: meLoading } = useMe();
  const isAdmin = me?.profile === UserProfile.ADMIN;

  const [values, setValues] = useState<AuditFilterValues>(EMPTY_FILTERS);
  const [showViews, setShowViews] = useState(false);

  const filters = useMemo<AuditFilters>(
    () => ({
      from: values.range?.[0]?.startOf("minute").toISOString(),
      to: rangeEnd(values.range?.[1] ?? null),
      // Sorted: the same selection always gives the same query key.
      events: [...values.events].sort(),
      excludeEvents: showViews ? undefined : [VIEWED_EVENT],
      levels: [...values.levels].sort() as AuditLevel[],
      user: values.user || undefined,
      q: values.query || undefined,
    }),
    [values, showViews],
  );

  const {
    data,
    error,
    isLoading,
    isFetching,
    isFetchingNextPage,
    hasNextPage,
    fetchNextPage,
  } = useAuditEntries(filters, isAdmin);
  const refresh = useAuditRefresh();

  const entries = useMemo(
    () => data?.pages.flatMap((p) => p.entries) ?? [],
    [data],
  );

  if (meLoading) {
    return (
      <Flex justify="center" style={{ padding: 48 }}>
        <Spin />
      </Flex>
    );
  }
  if (!isAdmin) {
    return <Result status="403" title={t("forbidden")} />;
  }

  return (
    <Flex gap="middle" vertical>
      <Flex justify="space-between" align="center" wrap gap="small">
        <PanelTitle>
          <SafetyCertificateOutlined /> {t("title")}
        </PanelTitle>
        <Button
          icon={<ReloadOutlined />}
          loading={isFetching && !isFetchingNextPage}
          onClick={refresh}
        >
          {t("refresh")}
        </Button>
      </Flex>
      <Text type="secondary">{t("readOnly")}</Text>

      <AuditFiltersBar values={values} onChange={setValues} />
      <Flex gap="small" align="center">
        <Switch
          size="small"
          checked={showViews}
          onChange={setShowViews}
          aria-label={t("filters.showViews")}
        />
        <Text>{t("filters.showViews")}</Text>
      </Flex>

      {error && (
        <Alert type="error" showIcon message={getApiErrorMessage(error)} />
      )}

      <AuditTable
        entries={entries}
        loading={isLoading || (isFetching && !isFetchingNextPage)}
      />

      {hasNextPage && (
        <Flex justify="center">
          <Button loading={isFetchingNextPage} onClick={() => fetchNextPage()}>
            {t("loadMore")}
          </Button>
        </Flex>
      )}
      <Text type="secondary" style={{ textAlign: "center" }} aria-live="polite">
        {hasNextPage || entries.length === 0
          ? t("count", { count: entries.length })
          : t("countAll", { count: entries.length })}
      </Text>
    </Flex>
  );
}
