import { LoadingOutlined, WarningOutlined } from "@ant-design/icons";
import { Alert, Flex, theme } from "antd";
import { useMemo } from "react";
import { useTranslation } from "react-i18next";
import {
  HostingConfigModel,
  HostingSpec,
  HostingStackDetail,
} from "../../../Services/Hosting/Models/hosting.model";
import ComposeTab from "./ComposeTab";
import NetworksTab from "./NetworksTab";
import OperationTab from "./OperationTab";
import OverviewTab from "./OverviewTab";
import SettingsTab from "./SettingsTab";

export type DetailTabKey =
  "overview" | "settings" | "networks" | "compose" | "operation";

function hasSpec(
  stack: HostingStackDetail,
): stack is HostingStackDetail & { spec: HostingSpec } {
  return stack.spec !== null && stack.spec !== undefined;
}

/**
 * Tabs of the detail page. Memoised: the stack object only changes when the
 * polled data changes.
 */
export default function useDetailTabs(
  stack: HostingStackDetail | undefined,
  config: HostingConfigModel | undefined,
  /** follows an operation started from a tab (e.g. a network change) */
  trackOperation: (operationId: string) => void,
) {
  const { t } = useTranslation("hosting");
  const { token } = theme.useToken();
  const busy = stack?.operation?.state === "running";
  const failed = stack?.operation?.state === "failed";
  const phpDeprecated = (config?.phpVersions ?? []).some(
    (v) => v.tag === stack?.phpVersion && v.deprecated,
  );

  return useMemo(() => {
    if (!stack || !config) return [];
    return [
      {
        key: "overview",
        label: t("detail.tabs.overview"),
        children: (
          <OverviewTab
            stack={stack}
            sitesDirectory={config.sitesDirectory}
            phpDeprecated={phpDeprecated}
          />
        ),
      },
      {
        key: "settings",
        label: t("detail.tabs.settings"),
        children: hasSpec(stack) ? (
          <SettingsTab key={stack.name} stack={stack} config={config} />
        ) : (
          <Alert type="error" showIcon title={t("detail.noSpec")} />
        ),
      },
      {
        key: "networks",
        label: t("detail.tabs.networks"),
        children: hasSpec(stack) ? (
          <NetworksTab stack={stack} onOperationStarted={trackOperation} />
        ) : (
          <Alert type="error" showIcon title={t("detail.noSpec")} />
        ),
      },
      {
        key: "compose",
        label: t("detail.tabs.compose"),
        children: <ComposeTab stack={stack} />,
      },
      {
        key: "operation",
        label: (
          <Flex gap={6} align="center">
            {t("detail.tabs.operation")}
            {busy && <LoadingOutlined />}
            {failed && <WarningOutlined style={{ color: token.colorError }} />}
          </Flex>
        ),
        children: <OperationTab operation={stack.operation} />,
      },
    ];
  }, [
    stack,
    config,
    phpDeprecated,
    busy,
    failed,
    t,
    token.colorError,
    trackOperation,
  ]);
}
