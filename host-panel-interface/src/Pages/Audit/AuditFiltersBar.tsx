import { Button, DatePicker, Flex, Input, Select } from "antd";
import dayjs, { Dayjs, ManipulateType } from "dayjs";
import { memo, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import {
  AUDIT_CATEGORIES,
  AUDIT_EVENTS,
  AUDIT_LEVELS,
  AuditLevel,
} from "../../Services/Audit/Models/audit.model";
import { auditEventLabel, auditLevelLabel } from "./auditLabels";

const { RangePicker } = DatePicker;

export type AuditRange = [Dayjs | null, Dayjs | null] | null;

export interface AuditFilterValues {
  range: AuditRange;
  events: string[];
  levels: AuditLevel[];
  user: string;
  query: string;
}

export const EMPTY_FILTERS: AuditFilterValues = {
  range: null,
  events: [],
  levels: [],
  user: "",
  query: "",
};

/** Same bound as the API (longer text filters are refused). */
const MAX_TEXT_FILTER = 256;

const PRESETS: {
  key: "hour" | "day" | "week" | "month";
  amount: number;
  unit: ManipulateType;
}[] = [
  { key: "hour", amount: 1, unit: "hour" },
  { key: "day", amount: 1, unit: "day" },
  { key: "week", amount: 7, unit: "day" },
  { key: "month", amount: 30, unit: "day" },
];

interface TextFilterProps {
  value: string;
  placeholder: string;
  width: number;
  onApply: (v: string) => void;
}

/**
 * Text filter with its own draft state (typing does not re-render the
 * table); applied on Enter, search or blur.
 */
const TextFilter = memo(function TextFilter({
  value,
  placeholder,
  width,
  onApply,
}: TextFilterProps) {
  const [draft, setDraft] = useState(value);
  const [prevValue, setPrevValue] = useState(value);
  if (value !== prevValue) {
    // Reset from outside (e.g. "Reset" button).
    setPrevValue(value);
    setDraft(value);
  }
  const apply = (v: string) => {
    const trimmed = v.trim();
    if (trimmed !== value) onApply(trimmed);
  };
  return (
    <Input.Search
      allowClear
      maxLength={MAX_TEXT_FILTER}
      style={{ width }}
      placeholder={placeholder}
      aria-label={placeholder}
      value={draft}
      onChange={(e) => {
        setDraft(e.target.value);
        if (!e.target.value) apply("");
      }}
      onSearch={apply}
      onBlur={() => apply(draft)}
    />
  );
});

interface Props {
  values: AuditFilterValues;
  onChange: (values: AuditFilterValues) => void;
}

/** Date range, events, levels, user and full-text filters. */
function AuditFiltersBar({ values, onChange }: Readonly<Props>) {
  const { t } = useTranslation("audit");
  const set = <K extends keyof AuditFilterValues>(
    key: K,
    v: AuditFilterValues[K],
  ) => onChange({ ...values, [key]: v });

  // Values are functions: "last hour" is computed when clicked, not when
  // the page was opened.
  const presets = useMemo(
    () =>
      PRESETS.map(({ key, amount, unit }) => ({
        label: t(`filters.presets.${key}`),
        value: () =>
          [dayjs().subtract(amount, unit), dayjs()] as [Dayjs, Dayjs],
      })),
    [t],
  );

  const eventOptions = useMemo(
    () =>
      AUDIT_CATEGORIES.map((category) => ({
        label: t(`categories.${category}`),
        title: t(`categories.${category}`),
        options: Object.entries(AUDIT_EVENTS)
          .filter(([, c]) => c === category)
          .map(([event]) => ({
            value: event,
            label: auditEventLabel(t, event),
          })),
      })),
    [t],
  );

  const levelOptions = useMemo(
    () => AUDIT_LEVELS.map((l) => ({ value: l, label: auditLevelLabel(t, l) })),
    [t],
  );

  const hasFilters =
    values.range !== null ||
    values.events.length > 0 ||
    values.levels.length > 0 ||
    values.user !== "" ||
    values.query !== "";

  return (
    <Flex gap="small" wrap>
      <RangePicker
        showTime={{ format: "HH:mm" }}
        format="YYYY-MM-DD HH:mm"
        value={values.range}
        onChange={(v) => set("range", v)}
        presets={presets}
        allowEmpty={[true, true]}
        placeholder={[t("filters.from"), t("filters.to")]}
        aria-label={t("filters.dates")}
      />
      <Select
        mode="multiple"
        allowClear
        style={{ minWidth: 260, flex: 1 }}
        placeholder={t("filters.events")}
        aria-label={t("filters.events")}
        value={values.events}
        onChange={(v) => set("events", v)}
        options={eventOptions}
        optionFilterProp="label"
        maxTagCount="responsive"
      />
      <Select
        mode="multiple"
        allowClear
        style={{ minWidth: 160 }}
        placeholder={t("filters.levels")}
        aria-label={t("filters.levels")}
        value={values.levels}
        onChange={(v) => set("levels", v)}
        options={levelOptions}
      />
      <TextFilter
        value={values.user}
        width={220}
        placeholder={t("filters.user")}
        onApply={(v) => set("user", v)}
      />
      <TextFilter
        value={values.query}
        width={240}
        placeholder={t("filters.search")}
        onApply={(v) => set("query", v)}
      />
      <Button disabled={!hasFilters} onClick={() => onChange(EMPTY_FILTERS)}>
        {t("filters.reset")}
      </Button>
    </Flex>
  );
}

export default memo(AuditFiltersBar);
