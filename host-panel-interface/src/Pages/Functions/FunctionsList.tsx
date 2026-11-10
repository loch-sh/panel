import { PlusOutlined, WarningOutlined } from "@ant-design/icons";
import {
  Alert,
  Button,
  Card,
  Empty,
  Flex,
  Space,
  Table,
  Tag,
  Tooltip,
  Typography,
} from "antd";
import { ColumnsType } from "antd/es/table";
import type { TFunction } from "i18next";
import { useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { Link, useNavigate } from "react-router";
import { PanelTitle } from "../../Components/Base/PanelTitle";
import useFunctions from "../../Services/Functions/Hooks/useFunctions";
import {
  FunctionSummaryModel,
  FunctionsInfoModel,
  needsPurge,
} from "../../Services/Functions/Models/functions.model";
import { getApiErrorMessage } from "../../Utils/apiErrors";
import { NetworkLinks } from "../Networks/NetworkTags";
import DeleteFunctionModal, {
  DeleteFunctionTarget,
} from "./DeleteFunctionModal";
import FunctionActions from "./FunctionActions";
import {
  EgressTag,
  formatDeploymentDate,
  formatMB,
  functionPath,
  FUNCTIONS_PATH,
  FunctionStateBadge,
  runtimeVersionLabel,
} from "./functionLabels";
import TrademarkNotice from "./TrademarkNotice";
import useFunctionsGate from "./useFunctionsGate";

const { Text, Paragraph } = Typography;

function getColumns(
  t: TFunction<"functions">,
  lang: string | undefined,
  onDelete: (target: DeleteFunctionTarget) => void,
): ColumnsType<FunctionSummaryModel> {
  return [
    {
      title: t("columns.name"),
      dataIndex: "name",
      render: (name: string, fn) => (
        <Space size={4} wrap>
          <Link to={functionPath(name)} style={{ fontWeight: 600 }}>
            {name}
          </Link>
          {needsPurge(fn.state) && (
            <Tooltip title={t(`stateHints.${fn.state as "modified"}`)}>
              <Tag color="red" icon={<WarningOutlined />}>
                {t("tags.purgeOnly")}
              </Tag>
            </Tooltip>
          )}
        </Space>
      ),
    },
    {
      title: t("columns.runtime"),
      render: (_: unknown, fn) =>
        fn.runtime ? (
          runtimeVersionLabel(t, fn.runtime, fn.runtimeVersion)
        ) : (
          <Text type="secondary">—</Text>
        ),
    },
    {
      title: t("columns.state"),
      dataIndex: "state",
      render: (state: string, fn) => (
        <FunctionStateBadge state={state} health={fn.health} />
      ),
    },
    {
      title: t("columns.memory"),
      dataIndex: "memoryMB",
      responsive: ["md"],
      render: (mb: number) => (mb > 0 ? formatMB(mb) : "—"),
    },
    {
      title: t("columns.timeout"),
      dataIndex: "timeoutSeconds",
      responsive: ["md"],
      render: (s: number) => (s > 0 ? t("units.seconds", { count: s }) : "—"),
    },
    {
      title: t("columns.networks"),
      dataIndex: "networks",
      responsive: ["lg"],
      render: (networks: string[]) => <NetworkLinks networks={networks} />,
    },
    {
      title: t("columns.internet"),
      dataIndex: "egress",
      responsive: ["lg"],
      render: (egress: boolean, fn) =>
        needsPurge(fn.state) && !fn.runtime ? (
          <Text type="secondary">—</Text>
        ) : (
          <EgressTag egress={egress} />
        ),
    },
    {
      title: t("columns.deployment"),
      dataIndex: "deployment",
      responsive: ["md"],
      render: (id?: string) =>
        id ? (
          <Tooltip title={id}>
            <Text code>{id.slice(-8)}</Text>
          </Tooltip>
        ) : (
          <Text type="secondary">{t("noCode")}</Text>
        ),
    },
    {
      title: t("columns.deployedAt"),
      dataIndex: "deployment",
      key: "deployedAt",
      responsive: ["lg"],
      render: (id?: string) => formatDeploymentDate(id, lang),
    },
    {
      title: t("columns.actions"),
      key: "actions",
      render: (_: unknown, fn) => (
        <FunctionActions fn={fn} onDelete={onDelete} />
      ),
    },
  ];
}

/** What a function is, shown while there is none. */
function Presentation({
  disabled,
  onCreate,
}: Readonly<{ disabled: boolean; onCreate: () => void }>) {
  const { t } = useTranslation("functions");
  return (
    <Card>
      <Empty
        image={Empty.PRESENTED_IMAGE_SIMPLE}
        description={
          <Flex vertical align="center" gap="small">
            <Typography.Title level={5} style={{ margin: 0 }}>
              {t("presentation.title")}
            </Typography.Title>
            <Paragraph
              type="secondary"
              style={{ maxWidth: 640, marginBottom: 0 }}
            >
              {t("presentation.text")}
            </Paragraph>
            <Paragraph
              type="secondary"
              style={{ maxWidth: 640, marginBottom: 0 }}
            >
              {t("presentation.security")}
            </Paragraph>
          </Flex>
        }
      >
        <Button
          type="primary"
          icon={<PlusOutlined />}
          disabled={disabled}
          onClick={onCreate}
        >
          {t("createFirst")}
        </Button>
      </Empty>
    </Card>
  );
}

/** Memory and CPU used by every function, against the global budget. */
function Usage({ config }: Readonly<{ config: FunctionsInfoModel }>) {
  const { t, i18n } = useTranslation("functions");
  const lang = i18n.resolvedLanguage;
  return (
    <Text type="secondary">
      {t("usage.count", { count: config.count, max: config.max })}
      {" · "}
      {config.maxTotalMemoryMB > 0
        ? t("usage.memory", {
            used: formatMB(config.usedMemoryMB),
            max: formatMB(config.maxTotalMemoryMB),
          })
        : t("usage.memoryUnbounded", { used: formatMB(config.usedMemoryMB) })}
      {" · "}
      {config.maxTotalCpus > 0
        ? t("usage.cpus", {
            used: config.usedCpus.toLocaleString(lang),
            max: config.maxTotalCpus.toLocaleString(lang),
          })
        : t("usage.cpusUnbounded", {
            used: config.usedCpus.toLocaleString(lang),
          })}
    </Text>
  );
}

/** The functions (/functions). */
export default function FunctionsList() {
  const { t, i18n } = useTranslation("functions");
  const lang = i18n.resolvedLanguage;
  const navigate = useNavigate();
  const { blocked, config } = useFunctionsGate(t("title"));
  const { data: functions, isLoading, error } = useFunctions(!!config);
  const [deleteTarget, setDeleteTarget] = useState<DeleteFunctionTarget | null>(
    null,
  );
  const columns = useMemo(
    () => getColumns(t, lang, setDeleteTarget),
    [t, lang],
  );

  if (blocked) return blocked;
  if (!config) return null;
  const full = config.count >= config.max;
  const goToCreate = () => navigate(`${FUNCTIONS_PATH}/new`);
  const empty = !isLoading && !error && (functions ?? []).length === 0;

  return (
    <Flex vertical gap="middle">
      <Flex justify="space-between" align="center" wrap gap="small">
        <PanelTitle>{t("title")}</PanelTitle>
        <Tooltip
          title={full ? t("limitReached", { max: config.max }) : undefined}
        >
          <Button
            type="primary"
            icon={<PlusOutlined />}
            disabled={full}
            onClick={goToCreate}
          >
            {t("newFunction")}
          </Button>
        </Tooltip>
      </Flex>
      <Paragraph type="secondary" style={{ marginBottom: 0 }}>
        {t("intro")}
      </Paragraph>
      <Usage config={config} />
      {!config.runtimeReady && (
        <Alert type="warning" showIcon title={t("runtimeNotReady")} />
      )}
      {error && (
        <Alert type="error" showIcon title={getApiErrorMessage(error)} />
      )}
      {empty ? (
        <Presentation disabled={full} onCreate={goToCreate} />
      ) : (
        <Table<FunctionSummaryModel>
          columns={columns}
          dataSource={functions}
          loading={isLoading}
          rowKey="name"
          pagination={false}
          scroll={{ x: "max-content" }}
        />
      )}
      <TrademarkNotice />
      <DeleteFunctionModal
        target={deleteTarget}
        onClose={() => setDeleteTarget(null)}
      />
    </Flex>
  );
}
