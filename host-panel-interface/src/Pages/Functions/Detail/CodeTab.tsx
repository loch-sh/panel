import { RollbackOutlined, UploadOutlined } from "@ant-design/icons";
import {
  Alert,
  Button,
  Card,
  Flex,
  Modal,
  Progress,
  Table,
  Tag,
  Tooltip,
  Typography,
} from "antd";
import { ColumnsType } from "antd/es/table";
import { useQueryClient } from "@tanstack/react-query";
import { useEffect, useMemo, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { toast } from "react-toastify";
import {
  useFunctionActivate,
  useFunctionDeploy,
} from "../../../Services/Functions/Hooks/useFunctionMutations";
import {
  DeploymentModel,
  FunctionDetailModel,
  FunctionsInfoModel,
  isCustomRuntime,
} from "../../../Services/Functions/Models/functions.model";
import { functionsServiceKeys } from "../../../Services/Functions/functions.service";
import { getApiErrorMessage } from "../../../Utils/apiErrors";
import { formatBytes } from "../../../Utils/bytes";
import { deploymentWarnings, formatDeploymentDate } from "../functionLabels";
import ZipDropzone from "../ZipDropzone";

const { Text, Paragraph } = Typography;

/** Code of a function: upload a zip, activate or roll back a deployment. */
export default function CodeTab({
  fn,
  config,
}: Readonly<{ fn: FunctionDetailModel; config: FunctionsInfoModel }>) {
  const { t, i18n } = useTranslation("functions");
  const lang = i18n.resolvedLanguage;
  const { mutateAsync: deploy, isPending: deploying } = useFunctionDeploy();
  const { mutateAsync: activate, isPending: activating } =
    useFunctionActivate();
  const [progress, setProgress] = useState<{
    name: string;
    percent: number;
  } | null>(null);
  const abort = useRef<AbortController | null>(null);
  const client = useQueryClient();
  // An upload still running when the tab or page goes away is aborted.
  useEffect(() => () => abort.current?.abort(), []);
  const deployments = useMemo(() => fn.deployments ?? [], [fn.deployments]);
  const activeIndex = deployments.findIndex((d) => d.active);

  const upload = async (file: File) => {
    const controller = new AbortController();
    abort.current = controller;
    setProgress({ name: file.name, percent: 0 });
    try {
      const d = await deploy({
        name: fn.name,
        file,
        signal: controller.signal,
        onProgress: (percent) => setProgress({ name: file.name, percent }),
      });
      toast.success(t("code.deployed", { id: d.id }));
      for (const w of deploymentWarnings(t, d)) toast.warning(w);
    } catch (err) {
      if (controller.signal.aborted) {
        // The server may have received the whole zip and deployed it.
        client.invalidateQueries({
          queryKey: [functionsServiceKeys.function, fn.name],
        });
        client.invalidateQueries({
          queryKey: [functionsServiceKeys.deployments, fn.name],
        });
        toast.info(t("code.interrupted"));
      } else {
        toast.error(getApiErrorMessage(err));
      }
    } finally {
      abort.current = null;
      setProgress(null);
    }
  };

  const askActivate = (d: DeploymentModel, index: number) => {
    // The API lists them newest first: below the active one is older.
    const rollback = activeIndex >= 0 && index > activeIndex;
    const mode = rollback ? "rollback" : "activate";
    Modal.confirm({
      title: t(`code.${mode}Confirm.title`, { id: d.id }),
      content: t(`code.${mode}Confirm.content`),
      okText: t(`code.${mode}`),
      cancelText: t("actions.cancel", { ns: "common" }),
      onOk: async () => {
        try {
          await activate({ name: fn.name, id: d.id });
          toast.success(t("code.activated", { id: d.id }));
        } catch (err) {
          toast.error(getApiErrorMessage(err));
        }
      },
    });
  };

  const columns: ColumnsType<DeploymentModel> = [
    {
      title: t("code.columns.id"),
      dataIndex: "id",
      render: (id: string, d) => (
        <Flex gap={4} wrap align="center">
          <Text code>{id}</Text>
          {d.active && <Tag color="green">{t("code.active")}</Tag>}
          {deploymentWarnings(t, d).map((w) => (
            <Tooltip key={w} title={w}>
              <Tag color="orange">{t("code.warning")}</Tag>
            </Tooltip>
          ))}
        </Flex>
      ),
    },
    {
      title: t("code.columns.date"),
      dataIndex: "id",
      key: "date",
      render: (id: string) => formatDeploymentDate(id, lang),
    },
    {
      title: t("code.columns.files"),
      dataIndex: "files",
      responsive: ["md"],
      render: (n: number) => n.toLocaleString(lang),
    },
    {
      title: t("code.columns.size"),
      dataIndex: "bytes",
      responsive: ["md"],
      render: (bytes: number, d) => (
        <Tooltip title={t("code.zipSize", { size: formatBytes(d.zipSize) })}>
          {formatBytes(bytes)}
        </Tooltip>
      ),
    },
    {
      title: t("code.columns.sha256"),
      dataIndex: "zipSha256",
      responsive: ["lg"],
      render: (sha: string) => (
        <Tooltip title={sha}>
          <Text code copyable={{ text: sha }}>
            {sha.slice(0, 12)}
          </Text>
        </Tooltip>
      ),
    },
    {
      title: t("code.columns.actions"),
      key: "actions",
      render: (_: unknown, d, index) =>
        d.active ? null : (
          <Button
            size="small"
            icon={
              activeIndex >= 0 && index > activeIndex ? (
                <RollbackOutlined />
              ) : (
                <UploadOutlined />
              )
            }
            disabled={activating || deploying}
            onClick={() => askActivate(d, index)}
          >
            {activeIndex >= 0 && index > activeIndex
              ? t("code.rollback")
              : t("code.activate")}
          </Button>
        ),
    },
  ];

  return (
    <Flex vertical gap="middle">
      <Card title={t("code.uploadTitle")}>
        <Flex vertical gap="small">
          <Paragraph type="secondary" style={{ marginBottom: 0 }}>
            {isCustomRuntime(fn.runtime)
              ? t("code.uploadIntroCustom", { arch: fn.arch })
              : t("code.uploadIntro", { handler: fn.handler })}
          </Paragraph>
          {progress ? (
            <Flex vertical gap={4}>
              <Text>
                {progress.percent >= 100
                  ? t("code.deploying", { name: progress.name })
                  : t("code.uploading", { name: progress.name })}
              </Text>
              <Progress percent={progress.percent} />
              <Button
                size="small"
                style={{ alignSelf: "flex-start" }}
                // Once the body is sent, the server is deploying it: too late.
                disabled={progress.percent >= 100}
                onClick={() => abort.current?.abort()}
              >
                {t("actions.cancel", { ns: "common" })}
              </Button>
            </Flex>
          ) : (
            <ZipDropzone
              maxBytes={config.maxCodeBytes}
              disabled={deploying || activating}
              onFile={(file) => void upload(file)}
            />
          )}
        </Flex>
      </Card>
      <Card
        title={t("code.deploymentsTitle")}
        styles={{ body: { padding: 0 } }}
      >
        <Alert
          type="info"
          showIcon
          banner
          title={t("code.kept", { count: config.deploymentsKept })}
        />
        <Table<DeploymentModel>
          columns={columns}
          dataSource={deployments}
          rowKey="id"
          pagination={false}
          scroll={{ x: "max-content" }}
          locale={{ emptyText: t("code.empty") }}
        />
      </Card>
    </Flex>
  );
}
