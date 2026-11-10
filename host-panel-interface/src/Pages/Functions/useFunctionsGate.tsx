import { Alert, Flex, Result, Skeleton } from "antd";
import { ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { PanelTitle } from "../../Components/Base/PanelTitle";
import useFunctionsConfig from "../../Services/Functions/Hooks/useFunctionsConfig";
import { FunctionsInfoModel } from "../../Services/Functions/Models/functions.model";
import useMe from "../../Services/Users/Hooks/useMe";
import { UserProfile } from "../../Services/Users/Models/users.model";
import { getApiErrorMessage } from "../../Utils/apiErrors";

/**
 * Admin and module checks shared by the functions pages: `blocked` is what
 * to render instead of the page (loading, forbidden, disabled), `config`
 * the module configuration otherwise.
 */
export default function useFunctionsGate(title: string): {
  blocked: ReactNode | null;
  config?: FunctionsInfoModel;
} {
  const { t } = useTranslation("functions");
  const { data: me, isLoading: meLoading } = useMe();
  const isAdmin = me?.profile === UserProfile.ADMIN;
  const { data, isLoading, error } = useFunctionsConfig(isAdmin);

  if (meLoading || (isAdmin && isLoading)) {
    return { blocked: <Skeleton active /> };
  }
  if (!isAdmin) {
    return { blocked: <Result status="403" title={t("forbidden")} /> };
  }
  if (error) {
    return {
      blocked: (
        <Alert type="error" showIcon title={getApiErrorMessage(error)} />
      ),
    };
  }
  if (!data?.enabled || !data.config) {
    return {
      blocked: (
        <Flex vertical gap="middle">
          <PanelTitle>{title}</PanelTitle>
          <Alert
            type={data?.error ? "error" : "info"}
            showIcon
            title={data?.error ? t("configError") : t("disabled")}
          />
        </Flex>
      ),
    };
  }
  return { blocked: null, config: data.config };
}
