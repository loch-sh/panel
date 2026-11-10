import { Flex, Typography } from "antd";
import { useTranslation } from "react-i18next";
import { PhpSettings } from "../../../Services/Hosting/Models/hosting.model";

/**
 * PHP memory limits: web requests (PHP-FPM) and command line (crons,
 * console). An empty web limit means "same as the CLI limit".
 */
export default function PhpMemorySummary({
  php,
}: Readonly<{ php?: PhpSettings | null }>) {
  const { t } = useTranslation("hosting");
  if (!php) return null;
  const cli = php.memoryLimit;
  const web = (php.webMemoryLimit ?? "").trim();
  return (
    <Flex vertical gap={2}>
      <span>
        {t("detail.overview.phpMemoryWeb")}{" "}
        {web ? (
          <Typography.Text code>{web}</Typography.Text>
        ) : (
          <Typography.Text type="secondary">
            {t("detail.overview.sameAsCli", { value: cli })}
          </Typography.Text>
        )}
      </span>
      <span>
        {t("detail.overview.phpMemoryCli")}{" "}
        <Typography.Text code>{cli}</Typography.Text>
      </span>
    </Flex>
  );
}
