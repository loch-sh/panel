import { Button, Result } from "antd";
import { isAxiosError } from "axios";
import { useTranslation } from "react-i18next";
import { getApiErrorMessage } from "../../Utils/apiErrors";

interface Props {
  /** error of the config query, if any */
  error?: unknown;
  onRetry?: () => void;
}

/**
 * Shown when the hosting plugin is off, or to users without access (403).
 * Any other config error (network, 5xx) is shown with a retry button.
 */
export default function HostingDisabled({ error, onRetry }: Readonly<Props>) {
  const { t } = useTranslation("hosting");
  const forbidden = isAxiosError(error) && error.response?.status === 403;

  if (error && !forbidden) {
    return (
      <Result
        status="error"
        title={t("configError")}
        subTitle={getApiErrorMessage(error)}
        extra={onRetry && <Button onClick={onRetry}>{t("retry")}</Button>}
      />
    );
  }
  return <Result status="info" title={t("disabled")} />;
}
