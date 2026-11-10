import { Space, Tag, Tooltip } from "antd";
import { format, parseISO } from "date-fns";
import { useTranslation } from "react-i18next";
import { useDateFnsLocale } from "../../i18n/useLocales";
import { PhpVersionModel } from "../../Services/Hosting/Models/hosting.model";

interface Props {
  version: PhpVersionModel;
}

/** Today as YYYY-MM-DD, comparable with the ISO dates of the catalog. */
function today(): string {
  return format(new Date(), "yyyy-MM-dd");
}

/**
 * End of active support (bug fixes) and end of security support of a PHP
 * version, as colored tags. Support lasts through the end date.
 */
export default function PhpSupportTags({ version }: Readonly<Props>) {
  const { t } = useTranslation("hosting");
  const locale = useDateFnsLocale();
  const now = today();
  const formatDate = (iso: string) => format(parseISO(iso), "P", { locale });

  // Dates may be missing (older API, unknown branch): show only those known.
  const active = version.activeSupportUntil;
  const security = version.securitySupportUntil;
  if (!active && !security) return null;
  const activeEnded = !!active && active < now;
  const securityEnded = !!security && security < now;

  return (
    <Space size={4} wrap>
      {active && (
        <Tooltip title={t("php.support.activeTooltip")}>
          <Tag color={activeEnded ? "default" : "green"}>
            {t(
              activeEnded
                ? "php.support.activeEnded"
                : "php.support.activeUntil",
              {
                date: formatDate(active),
              },
            )}
          </Tag>
        </Tooltip>
      )}
      {security && (
        <Tooltip title={t("php.support.securityTooltip")}>
          <Tag color={securityEnded ? "red" : activeEnded ? "orange" : "blue"}>
            {t(
              securityEnded
                ? "php.support.securityEnded"
                : "php.support.securityUntil",
              { date: formatDate(security) },
            )}
          </Tag>
        </Tooltip>
      )}
    </Space>
  );
}
