import { ArrowLeftOutlined } from "@ant-design/icons";
import { Alert, Button, Flex, Result, Skeleton, Tabs } from "antd";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { useNavigate, useParams, useSearchParams } from "react-router";
import { PanelTitle } from "../../Components/Base/PanelTitle";
import useFunction, {
  isFunctionNotFound,
} from "../../Services/Functions/Hooks/useFunction";
import {
  FunctionsInfoModel,
  needsPurge,
} from "../../Services/Functions/Models/functions.model";
import { getApiErrorMessage } from "../../Utils/apiErrors";
import CodeTab from "./Detail/CodeTab";
import ConfigurationTab from "./Detail/ConfigurationTab";
import CredentialsTab from "./Detail/CredentialsTab";
import NetworksTab from "./Detail/NetworksTab";
import OverviewTab from "./Detail/OverviewTab";
import SecretsTab from "./Detail/SecretsTab";
import TestTab from "./Detail/TestTab";
import TriggersTab from "./Detail/TriggersTab";
import DeleteFunctionModal, {
  DeleteFunctionTarget,
} from "./DeleteFunctionModal";
import FunctionActions from "./FunctionActions";
import { FUNCTIONS_PATH, FunctionStateBadge } from "./functionLabels";
import useFunctionsGate from "./useFunctionsGate";

const TAB_KEYS = [
  "overview",
  "code",
  "test",
  "configuration",
  "secrets",
  "credentials",
  "networks",
  "triggers",
] as const;
type TabKey = (typeof TAB_KEYS)[number];
const isTabKey = (v: string | null): v is TabKey =>
  (TAB_KEYS as readonly (string | null)[]).includes(v);

/** A function (/functions/:name), its tab in `?tab=`. */
export default function FunctionDetail() {
  const { t } = useTranslation("functions");
  const { blocked, config } = useFunctionsGate(t("title"));
  if (blocked) return blocked;
  return config ? <Detail config={config} /> : null;
}

function Detail({ config }: Readonly<{ config: FunctionsInfoModel }>) {
  const { t } = useTranslation("functions");
  const { name = "" } = useParams();
  const navigate = useNavigate();
  const [params, setParams] = useSearchParams();
  const tabParam = params.get("tab");
  const tab: TabKey = isTabKey(tabParam) ? tabParam : "overview";
  const { data: fn, isLoading, error } = useFunction(name);
  const [deleteTarget, setDeleteTarget] = useState<DeleteFunctionTarget | null>(
    null,
  );

  if (isLoading) return <Skeleton active />;
  if (isFunctionNotFound(error) || (!fn && !error)) {
    return (
      <Result
        status="404"
        title={t("detail.notFound")}
        extra={
          <Button onClick={() => navigate(FUNCTIONS_PATH)}>
            {t("detail.back")}
          </Button>
        }
      />
    );
  }
  if (!fn) {
    return <Alert type="error" showIcon title={getApiErrorMessage(error)} />;
  }

  const purgeOnly = needsPurge(fn.state);
  const tabs = [
    {
      key: "overview",
      label: t("detail.tabs.overview"),
      children: <OverviewTab fn={fn} />,
    },
    {
      key: "code",
      label: t("detail.tabs.code"),
      disabled: purgeOnly,
      children: <CodeTab fn={fn} config={config} />,
    },
    {
      key: "test",
      label: t("detail.tabs.test"),
      disabled: purgeOnly,
      children: <TestTab fn={fn} config={config} />,
    },
    {
      key: "configuration",
      label: t("detail.tabs.configuration"),
      disabled: purgeOnly,
      children: <ConfigurationTab key={fn.name} fn={fn} config={config} />,
    },
    {
      key: "secrets",
      label: t("detail.tabs.secrets"),
      disabled: purgeOnly,
      children: <SecretsTab fn={fn} />,
    },
    {
      key: "credentials",
      label: t("detail.tabs.credentials"),
      disabled: purgeOnly,
      children: <CredentialsTab fn={fn} />,
    },
    {
      key: "networks",
      label: t("detail.tabs.networks"),
      disabled: purgeOnly,
      children: <NetworksTab fn={fn} />,
    },
    {
      key: "triggers",
      label: t("detail.tabs.triggers"),
      children: <TriggersTab fn={fn} />,
    },
  ];

  return (
    <Flex vertical gap="middle" style={{ maxWidth: 1200 }}>
      <Flex justify="space-between" align="center" wrap gap="small">
        <Flex gap="small" align="center" wrap>
          <Button
            type="text"
            icon={<ArrowLeftOutlined />}
            aria-label={t("detail.back")}
            onClick={() => navigate(FUNCTIONS_PATH)}
          />
          <PanelTitle>{fn.name}</PanelTitle>
          <FunctionStateBadge state={fn.state} health={fn.health} />
        </Flex>
        <FunctionActions fn={fn} onDelete={setDeleteTarget} showTest={false} />
      </Flex>
      <Tabs
        activeKey={purgeOnly && tab !== "triggers" ? "overview" : tab}
        onChange={(key) =>
          setParams(key === "overview" ? {} : { tab: key }, { replace: true })
        }
        items={tabs}
      />
      <DeleteFunctionModal
        target={deleteTarget}
        onClose={() => setDeleteTarget(null)}
        onDeleted={() => navigate(FUNCTIONS_PATH)}
      />
    </Flex>
  );
}
