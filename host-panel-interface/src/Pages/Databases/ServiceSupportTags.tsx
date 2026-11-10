import { PushpinOutlined, WarningOutlined } from "@ant-design/icons";
import { Flex, Tag, Tooltip } from "antd";
import { format, parseISO } from "date-fns";
import { useTranslation } from "react-i18next";
import { useDateFnsLocale } from "../../i18n/useLocales";

/** Support of a version (catalog entry or the version of an instance). */
export interface ServiceSupport {
  supportUntil?: string;
  activeSupportUntil?: string;
  /** the security support is over (computed by the API) */
  deprecated?: boolean;
  /** offered because the server configuration pins it */
  pinned?: boolean;
}

/** Something to show: a known date, the end of support or a pin. */
export function hasSupportInfo(s: ServiceSupport): boolean {
  return !!(s.supportUntil || s.activeSupportUntil || s.deprecated || s.pinned);
}

/** Today as YYYY-MM-DD, comparable with the ISO dates of the catalog. */
function today(): string {
  return format(new Date(), "yyyy-MM-dd");
}

/**
 * End of the active support (bug fixes) and of the security support of a
 * service version, as tags; unknown dates are not shown. Support lasts
 * through the end date.
 */
export default function ServiceSupportTags({
  support,
}: Readonly<{ support: ServiceSupport }>) {
  const { t } = useTranslation("databases");
  const locale = useDateFnsLocale();
  const { activeSupportUntil: active, supportUntil: security } = support;
  if (!hasSupportInfo(support)) return null;
  const now = today();
  const formatDate = (iso: string) => {
    const d = parseISO(iso);
    return Number.isNaN(d.getTime()) ? iso : format(d, "P", { locale });
  };
  const activeEnded = !!active && active < now;
  const securityEnded = support.deprecated || (!!security && security < now);

  // Short labels ("Support: date", "Security: date"); the full sentence and
  // what it means are in the tooltip, the end of a support in the color.
  const tip = (sentence: string, meaning: string) => (
    <>
      {sentence}
      <br />
      {meaning}
    </>
  );

  return (
    <Flex gap={4} wrap>
      {active && (
        <Tooltip
          title={tip(
            t(activeEnded ? "support.activeEnded" : "support.activeUntil", {
              date: formatDate(active),
            }),
            t("support.activeTooltip"),
          )}
        >
          <Tag color={activeEnded ? "default" : "green"}>
            {t("support.active", { date: formatDate(active) })}
          </Tag>
        </Tooltip>
      )}
      {securityEnded ? (
        <Tooltip
          title={
            security
              ? tip(
                  t("support.securityEnded", { date: formatDate(security) }),
                  t("support.securityEndedTooltip"),
                )
              : t("support.securityEndedTooltip")
          }
        >
          <Tag color="error" icon={<WarningOutlined />}>
            {security
              ? t("support.security", { date: formatDate(security) })
              : t("support.ended")}
          </Tag>
        </Tooltip>
      ) : (
        security && (
          <Tooltip
            title={tip(
              t("support.securityUntil", { date: formatDate(security) }),
              t("support.securityTooltip"),
            )}
          >
            <Tag color={activeEnded ? "orange" : "blue"}>
              {t("support.security", { date: formatDate(security) })}
            </Tag>
          </Tooltip>
        )
      )}
      {support.pinned && (
        <Tooltip title={t("support.pinnedTooltip")}>
          <Tag color="cyan" icon={<PushpinOutlined />}>
            {t("support.pinned")}
          </Tag>
        </Tooltip>
      )}
    </Flex>
  );
}
