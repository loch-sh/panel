import { CopyOutlined, DeleteOutlined, PlusOutlined } from "@ant-design/icons";
import {
  Button,
  Checkbox,
  Flex,
  Form,
  Input,
  InputNumber,
  Modal,
  Popconfirm,
  Table,
  Tag,
  Tooltip,
  Typography,
  message,
} from "antd";
import { ColumnsType } from "antd/es/table";
import type { Locale } from "date-fns";
import type { TFunction } from "i18next";
import { useCallback, useMemo, useRef, useState } from "react";
import { Trans, useTranslation } from "react-i18next";
import { getApiErrorMessage } from "../../Utils/apiErrors";
import { PanelTitle } from "../../Components/Base/PanelTitle";
import useApiKeys from "../../Services/ApiKeys/Hooks/useApiKeys";
import useCreateApiKey from "../../Services/ApiKeys/Hooks/useCreateApiKey";
import useDeleteApiKey from "../../Services/ApiKeys/Hooks/useDeleteApiKey";
import useSftpConfig from "../../Services/Sftp/Hooks/useSftpConfig";
import useHostingConfig from "../../Services/Hosting/Hooks/useHostingConfig";
import useServicesConfig from "../../Services/Databases/Hooks/useServicesConfig";
import useMe from "../../Services/Users/Hooks/useMe";
import { UserProfile } from "../../Services/Users/Models/users.model";
import {
  API_KEY_SCOPES,
  ApiKeyModel,
} from "../../Services/ApiKeys/Models/apikeys.model";
import { apiKeyScopeLabel } from "../../Services/ApiKeys/apikeys.scopes";
import { format, formatDistanceToNow } from "date-fns";
import { appConfig } from "../../config";
import { useDateFnsLocale } from "../../i18n/useLocales";

const MIN_VALIDITY_DAYS = 1;
const MAX_VALIDITY_DAYS = 365;

function getColumns(params: {
  t: TFunction<"apikeys">;
  locale: Locale;
  onDelete: (id: string) => void;
  disableActions: boolean;
}): ColumnsType<ApiKeyModel> {
  const { t, locale } = params;
  return [
    {
      title: t("table.columns.name"),
      dataIndex: "name",
    },
    {
      title: t("table.columns.prefix"),
      dataIndex: "keyPrefix",
      responsive: ["md"],
      render: (value: string) => (
        <Typography.Text code>{value}...</Typography.Text>
      ),
    },
    {
      title: t("table.columns.permissions"),
      dataIndex: "scopes",
      responsive: ["md"],
      render: (scopes?: string[]) => {
        if (!scopes || scopes.length === 0 || scopes.includes("*")) {
          return <Tag color="gold">{t("table.allScopes")}</Tag>;
        }
        return (
          <Flex gap={4} wrap="wrap">
            {scopes.map((s) => (
              <Tag key={s}>{apiKeyScopeLabel(t, s)}</Tag>
            ))}
          </Flex>
        );
      },
    },
    {
      title: t("table.columns.expiresAt"),
      dataIndex: "expiresAt",
      render: (value: string) => {
        const date = new Date(value);
        const isExpired = date < new Date();
        return (
          <Typography.Text type={isExpired ? "danger" : undefined}>
            {format(date, "P", { locale })}
            {isExpired && ` ${t("table.expired")}`}
          </Typography.Text>
        );
      },
    },
    {
      title: t("table.columns.createdAt"),
      dataIndex: "createdAt",
      responsive: ["md"],
      render: (value: string) => format(new Date(value), "P", { locale }),
    },
    {
      title: t("table.columns.lastUsed"),
      dataIndex: "lastUsedAt",
      responsive: ["md"],
      render: (value?: string) =>
        value
          ? formatDistanceToNow(new Date(value), {
              addSuffix: true,
              locale,
            })
          : t("status.never", { ns: "common" }),
    },
    {
      title: t("table.columns.actions"),
      render: (_value, record) => (
        <Popconfirm
          title={t("revoke.title")}
          description={t("revoke.description")}
          onConfirm={() => params.onDelete(record.id)}
          okText={t("revoke.action")}
          cancelText={t("actions.cancel", { ns: "common" })}
          okButtonProps={{ danger: true }}
        >
          <Tooltip title={t("revoke.action")}>
            <Button
              danger
              disabled={params.disableActions}
              shape="circle"
              icon={<DeleteOutlined />}
            />
          </Tooltip>
        </Popconfirm>
      ),
    },
  ];
}

interface FormValues {
  name: string;
  expiresInDays: number;
  scopes: string[];
}

export default function ApiKeys() {
  const { t } = useTranslation("apikeys");
  const dateLocale = useDateFnsLocale();
  const { data, isLoading } = useApiKeys();
  const { mutateAsync: createKey, isPending: pendingCreate } =
    useCreateApiKey();
  const { mutate: deleteKey, isPending: pendingDelete } = useDeleteApiKey({
    onError: (err) => message.error(getApiErrorMessage(err)),
  });

  // SFTP, hosting and services scopes only make sense when the plugin is on
  // (admin-only endpoints: restricted users get no config and never see them).
  const { data: me } = useMe();
  const isAdmin = me?.profile === UserProfile.ADMIN;
  const { data: sftpConfig } = useSftpConfig(isAdmin);
  // Admin-only endpoints: not called for restricted users (guaranteed 403).
  const { data: hostingConfig } = useHostingConfig(isAdmin);
  const { data: servicesConfig } = useServicesConfig(isAdmin);
  const sftpEnabled = sftpConfig?.enabled ?? false;
  const hostingEnabled = hostingConfig?.enabled ?? false;
  const servicesEnabled = servicesConfig?.enabled ?? false;
  const availableScopes = useMemo(
    () =>
      API_KEY_SCOPES.filter(
        (scope) =>
          (sftpEnabled || !scope.startsWith("sftp:")) &&
          (hostingEnabled || !scope.startsWith("hosting:")) &&
          (servicesEnabled || !scope.startsWith("services:")) &&
          // Networks are admin only (and always available to admins).
          (isAdmin || !scope.startsWith("networks:")) &&
          (isAdmin || scope !== "audit:read"),
      ),
    [sftpEnabled, hostingEnabled, servicesEnabled, isAdmin],
  );

  const [isModalOpen, setIsModalOpen] = useState(false);
  const [createdToken, setCreatedToken] = useState<string | null>(null);
  const [form] = Form.useForm<FormValues>();
  const prevScopes = useRef<string[]>(["*"]);

  const handleScopesChange = useCallback(
    (values: string[]) => {
      const hadWildcard = prevScopes.current.includes("*");
      const hasWildcard = values.includes("*");

      let next: string[];
      if (hasWildcard && !hadWildcard) {
        // Wildcard just selected → keep only wildcard
        next = ["*"];
      } else if (hadWildcard && values.length > 1) {
        // Had wildcard, user added something else → remove wildcard
        next = values.filter((v) => v !== "*");
      } else {
        next = values;
      }

      prevScopes.current = next;
      form.setFieldsValue({ scopes: next });
    },
    [form],
  );

  const handleCreate = useCallback(async () => {
    try {
      const values = await form.validateFields();
      const result = await createKey(values);
      setCreatedToken(result.token);
      form.resetFields();
      prevScopes.current = ["*"];
      setIsModalOpen(false);
    } catch (err: unknown) {
      // Suppress Ant Design form validation errors; surface other errors
      if (err && typeof err === "object" && "errorFields" in err) return;
      message.error(getApiErrorMessage(err, t("create.failed")));
    }
  }, [form, createKey, t]);

  const handleDelete = useCallback(
    (id: string) => {
      deleteKey({ id });
    },
    [deleteKey],
  );

  const handleCopy = useCallback(() => {
    if (createdToken) {
      navigator.clipboard.writeText(createdToken);
      message.success(t("created.copied"));
    }
  }, [createdToken, t]);

  const columns = useMemo(
    () =>
      getColumns({
        t,
        locale: dateLocale,
        onDelete: handleDelete,
        disableActions: pendingDelete,
      }),
    [t, dateLocale, handleDelete, pendingDelete],
  );

  return (
    <>
      <Flex gap="middle" vertical>
        <Flex justify="space-between" align="center">
          <PanelTitle>{t("title")}</PanelTitle>
          <Button
            type="primary"
            icon={<PlusOutlined />}
            onClick={() => setIsModalOpen(true)}
          >
            {t("newKey")}
          </Button>
        </Flex>

        <Table
          columns={columns}
          dataSource={data}
          loading={isLoading}
          rowKey="id"
          locale={{ emptyText: t("empty") }}
          scroll={{ x: "max-content" }}
        />
      </Flex>

      {/* Create modal */}
      <Modal
        title={t("create.title")}
        open={isModalOpen}
        onOk={handleCreate}
        onCancel={() => {
          setIsModalOpen(false);
          form.resetFields();
        }}
        confirmLoading={pendingCreate}
        okText={t("actions.create", { ns: "common" })}
        cancelText={t("actions.cancel", { ns: "common" })}
      >
        <Form
          form={form}
          layout="vertical"
          initialValues={{ expiresInDays: 90, scopes: ["*"] }}
        >
          <Form.Item
            name="name"
            label={t("create.form.name.label")}
            rules={[
              { required: true, message: t("create.form.name.required") },
            ]}
            help={t("create.form.name.help")}
          >
            <Input placeholder={t("create.form.name.placeholder")} />
          </Form.Item>
          <Form.Item
            name="expiresInDays"
            label={t("create.form.expiresInDays.label")}
            rules={[
              {
                required: true,
                message: t("create.form.expiresInDays.required"),
              },
              {
                type: "number",
                min: MIN_VALIDITY_DAYS,
                max: MAX_VALIDITY_DAYS,
                message: t("create.form.expiresInDays.range", {
                  min: MIN_VALIDITY_DAYS,
                  max: MAX_VALIDITY_DAYS,
                }),
              },
            ]}
            help={t("create.form.expiresInDays.help", {
              max: MAX_VALIDITY_DAYS,
            })}
          >
            <InputNumber
              min={MIN_VALIDITY_DAYS}
              max={MAX_VALIDITY_DAYS}
              style={{ width: "100%" }}
            />
          </Form.Item>
          <Form.Item
            name="scopes"
            label={t("create.form.scopes.label")}
            rules={[
              {
                required: true,
                message: t("create.form.scopes.required"),
              },
            ]}
          >
            <Checkbox.Group
              onChange={(values) => handleScopesChange(values as string[])}
              style={{ display: "flex", flexDirection: "column", gap: 8 }}
            >
              {availableScopes.map((scope) => (
                <Checkbox key={scope} value={scope}>
                  {apiKeyScopeLabel(t, scope)}
                </Checkbox>
              ))}
            </Checkbox.Group>
          </Form.Item>
        </Form>
      </Modal>

      {/* Token display modal (shown once after creation) */}
      <Modal
        title={t("created.title")}
        open={createdToken !== null}
        onOk={() => setCreatedToken(null)}
        onCancel={() => setCreatedToken(null)}
        cancelButtonProps={{ style: { display: "none" } }}
        okText={t("created.ok")}
      >
        <Flex vertical gap="middle">
          <Typography.Paragraph type="warning">
            {t("created.warning")}
          </Typography.Paragraph>
          <Input.Search
            value={createdToken ?? ""}
            readOnly
            enterButton={<CopyOutlined />}
            onSearch={handleCopy}
          />
          <Typography.Paragraph type="secondary">
            <Trans
              t={t}
              i18nKey="created.cliHelp"
              values={{
                serverName: t("created.serverName"),
                apiUrl: appConfig.apiUrl,
              }}
              components={{
                configCmd: <Typography.Text code />,
                loginCmd: <Typography.Text code />,
              }}
            />
          </Typography.Paragraph>
        </Flex>
      </Modal>
    </>
  );
}
