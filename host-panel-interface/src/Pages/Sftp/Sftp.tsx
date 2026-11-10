import {
  CopyOutlined,
  DeleteOutlined,
  EditFilled,
  FolderOutlined,
  KeyOutlined,
  PauseCircleOutlined,
  PlayCircleOutlined,
  PlusOutlined,
} from "@ant-design/icons";
import {
  Button,
  Descriptions,
  Flex,
  Form,
  Input,
  Modal,
  Space,
  Table,
  Tag,
  Tooltip,
  Typography,
  message,
} from "antd";
import { ColumnsType } from "antd/es/table";
import { useCallback, useMemo, useState } from "react";
import { PanelTitle } from "../../Components/Base/PanelTitle";
import useSftpConfig from "../../Services/Sftp/Hooks/useSftpConfig";
import useSftpSites from "../../Services/Sftp/Hooks/useSftpSites";
import useHostingConfig from "../../Services/Hosting/Hooks/useHostingConfig";
import useHostingSites from "../../Services/Hosting/Hooks/useHostingSites";
import useSftpUsers from "../../Services/Sftp/Hooks/useSftpUsers";
import useSftpUserCreate from "../../Services/Sftp/Hooks/useSftpUserCreate";
import useSftpUserUpdate from "../../Services/Sftp/Hooks/useSftpUserUpdate";
import useSftpUserDelete from "../../Services/Sftp/Hooks/useSftpUserDelete";
import useSftpResetPassword from "../../Services/Sftp/Hooks/useSftpResetPassword";
import useSftpToggleEnabled from "../../Services/Sftp/Hooks/useSftpToggleEnabled";
import {
  SftpSiteModel,
  SftpUserCreatePayload,
  SftpUserModel,
  SftpUserUpdatePayload,
} from "../../Services/Sftp/Models/sftp.model";
import { getApiErrorMessage } from "../../Utils/apiErrors";
import { formatBytes } from "../../Utils/bytes";
import { formatDistanceToNow, Locale } from "date-fns";
import { TFunction } from "i18next";
import { useTranslation } from "react-i18next";
import { useDateFnsLocale } from "../../i18n/useLocales";
import { toast } from "react-toastify";
import SftpUserFormModal from "./SftpUserFormModal";

const { Text } = Typography;

function formatLastLogin(ms: number, locale: Locale, never: string): string {
  if (!ms) return never;
  return formatDistanceToNow(new Date(ms), { addSuffix: true, locale });
}

function getUserColumns(params: {
  domain?: string;
  port?: string;
  onEdit: (user: SftpUserModel) => void;
  onResetPassword: (user: SftpUserModel) => void;
  onToggleEnabled: (user: SftpUserModel) => void;
  onDelete: (user: SftpUserModel) => void;
  onCopyConnection: (user: SftpUserModel) => void;
  disableActions: boolean;
  t: TFunction<"sftp">;
  dateLocale: Locale;
}): ColumnsType<SftpUserModel> {
  const { t } = params;
  return [
    {
      title: t("users.columns.user"),
      dataIndex: "username",
      render: (value: string, record) => (
        <Space>
          <Text strong>{value}</Text>
          {record.subPath && (
            <Tooltip title={t("users.subPathTooltip")}>
              <Tag color="blue" icon={<FolderOutlined />}>
                {record.subPath}
              </Tag>
            </Tooltip>
          )}
          {record.status !== 1 && (
            <Tag color="default">{t("status.disabled", { ns: "common" })}</Tag>
          )}
          {record.requirePasswordChange && (
            <Tag color="orange">{t("users.passwordChangeRequired")}</Tag>
          )}
        </Space>
      ),
    },
    {
      title: t("users.columns.lastLogin"),
      dataIndex: "lastLogin",
      responsive: ["md"],
      render: (value: number) => (
        <Text style={{ fontSize: 12 }}>
          {formatLastLogin(
            value,
            params.dateLocale,
            t("status.never", { ns: "common" }),
          )}
        </Text>
      ),
    },
    {
      title: t("users.columns.quota"),
      responsive: ["md"],
      render: (_: unknown, record) => {
        if (!record.quotaSize) {
          return (
            <Text type="secondary">
              {formatBytes(record.usedQuotaSize)} / ∞
            </Text>
          );
        }
        return (
          <Text>
            {formatBytes(record.usedQuotaSize)} /{" "}
            {formatBytes(record.quotaSize)}
          </Text>
        );
      },
    },
    {
      title: t("users.columns.actions"),
      width: 220,
      render: (_: unknown, record) => (
        <Space.Compact>
          <Tooltip title={t("actions.copyConnection")}>
            <Button
              shape="circle"
              variant="outlined"
              disabled={!params.domain}
              onClick={() => params.onCopyConnection(record)}
              icon={<CopyOutlined />}
            />
          </Tooltip>
          <Tooltip title={t("actions.edit", { ns: "common" })}>
            <Button
              shape="circle"
              type="primary"
              variant="outlined"
              disabled={params.disableActions}
              onClick={() => params.onEdit(record)}
              icon={<EditFilled />}
            />
          </Tooltip>
          <Tooltip title={t("actions.resetPassword")}>
            <Button
              shape="circle"
              variant="outlined"
              disabled={params.disableActions}
              onClick={() => params.onResetPassword(record)}
              icon={<KeyOutlined />}
            />
          </Tooltip>
          <Tooltip
            title={
              record.status === 1 ? t("actions.disable") : t("actions.enable")
            }
          >
            <Button
              shape="circle"
              variant="outlined"
              color={record.status === 1 ? "orange" : "green"}
              disabled={params.disableActions}
              onClick={() => params.onToggleEnabled(record)}
              icon={
                record.status === 1 ? (
                  <PauseCircleOutlined />
                ) : (
                  <PlayCircleOutlined />
                )
              }
            />
          </Tooltip>
          <Tooltip title={t("actions.delete", { ns: "common" })}>
            <Button
              danger
              shape="circle"
              disabled={params.disableActions}
              onClick={() => params.onDelete(record)}
              icon={<DeleteOutlined />}
            />
          </Tooltip>
        </Space.Compact>
      ),
    },
  ];
}

export default function Sftp() {
  const { t } = useTranslation("sftp");
  const dateLocale = useDateFnsLocale();
  const { data: config } = useSftpConfig();
  const enabled = config?.enabled ?? false;
  const { data: sites, isLoading: loadingSites } = useSftpSites(enabled);
  const { data: users, isLoading: loadingUsers } = useSftpUsers(enabled);

  const { mutateAsync: createUser, isPending: creating } = useSftpUserCreate();
  const { mutateAsync: updateUser, isPending: updating } = useSftpUserUpdate();
  const { mutateAsync: deleteUser, isPending: deleting } = useSftpUserDelete();
  const { mutateAsync: resetPassword, isPending: resetting } =
    useSftpResetPassword();
  const { mutateAsync: toggleEnabled, isPending: toggling } =
    useSftpToggleEnabled();

  const isPending = creating || updating || deleting || resetting || toggling;

  const [formOpen, setFormOpen] = useState(false);
  const [userToEdit, setUserToEdit] = useState<SftpUserModel | null>(null);
  const [defaultSite, setDefaultSite] = useState<string | undefined>();
  const [resetTarget, setResetTarget] = useState<SftpUserModel | null>(null);
  const [resetForm] = Form.useForm<{ password: string }>();

  const usersBySite = useMemo(() => {
    const map = new Map<string, SftpUserModel[]>();
    for (const u of users ?? []) {
      const list = map.get(u.site) ?? [];
      list.push(u);
      map.set(u.site, list);
    }
    return map;
  }, [users]);

  // Site directories created by hosted stacks (without SFTP user yet) can be
  // selected too.
  // SFTP being enabled implies an admin (both endpoints are admin-only).
  const { data: hostingConfig } = useHostingConfig(enabled);
  const { data: hostingSites } = useHostingSites(
    enabled && (hostingConfig?.enabled ?? false),
  );
  const siteNames = useMemo(
    () =>
      Array.from(
        new Set([
          ...(sites ?? []).map((s) => s.name),
          ...(hostingSites ?? []).map((s) => s.name),
        ]),
      ).sort((a, b) => a.localeCompare(b)),
    [sites, hostingSites],
  );

  const openCreate = useCallback((site?: string) => {
    setUserToEdit(null);
    setDefaultSite(site);
    setFormOpen(true);
  }, []);

  const openEdit = useCallback((user: SftpUserModel) => {
    setUserToEdit(user);
    setDefaultSite(undefined);
    setFormOpen(true);
  }, []);

  const closeForm = useCallback(() => {
    setFormOpen(false);
    setUserToEdit(null);
  }, []);

  const handleCreate = useCallback(
    async (payload: SftpUserCreatePayload) => {
      try {
        const created = await createUser(payload);
        toast.success(t("toasts.userCreated"));
        if (created.initDirsError) {
          toast.warning(t("toasts.initDirsFailed"));
        }
        closeForm();
      } catch (err) {
        toast.error(getApiErrorMessage(err));
      }
    },
    [createUser, closeForm, t],
  );

  const handleUpdate = useCallback(
    async (username: string, payload: SftpUserUpdatePayload) => {
      try {
        await updateUser({ username, ...payload });
        toast.success(t("toasts.userUpdated"));
        closeForm();
      } catch (err) {
        toast.error(getApiErrorMessage(err));
      }
    },
    [updateUser, closeForm, t],
  );

  const handleDelete = useCallback(
    (user: SftpUserModel) => {
      Modal.confirm({
        title: t("deleteConfirm.title"),
        content: t("deleteConfirm.content", { username: user.username }),
        okText: t("actions.delete", { ns: "common" }),
        okButtonProps: { danger: true },
        cancelText: t("actions.cancel", { ns: "common" }),
        onOk: () =>
          deleteUser(user.username).catch((err) =>
            toast.error(getApiErrorMessage(err)),
          ),
      });
    },
    [deleteUser, t],
  );

  const handleToggleEnabled = useCallback(
    (user: SftpUserModel) => {
      const willEnable = user.status !== 1;
      toggleEnabled({ username: user.username, enabled: willEnable }).catch(
        (err) => toast.error(getApiErrorMessage(err)),
      );
    },
    [toggleEnabled],
  );

  const handleCopyConnection = useCallback(
    (user: SftpUserModel) => {
      if (!config?.domain) return;
      const port = config.port ? `:${config.port}` : "";
      const conn = `sftp://${user.username}@${config.domain}${port}`;
      navigator.clipboard.writeText(conn);
      message.success(t("toasts.connectionCopied"));
    },
    [config, t],
  );

  const openReset = useCallback(
    (user: SftpUserModel) => {
      resetForm.resetFields();
      setResetTarget(user);
    },
    [resetForm],
  );

  const handleReset = useCallback(async () => {
    const values = await resetForm.validateFields();
    if (!resetTarget) return;
    try {
      await resetPassword({
        username: resetTarget.username,
        password: values.password,
      });
      toast.success(t("toasts.passwordReset"));
      setResetTarget(null);
    } catch (err) {
      toast.error(getApiErrorMessage(err));
    }
  }, [resetForm, resetTarget, resetPassword, t]);

  const siteColumns: ColumnsType<SftpSiteModel> = [
    {
      title: t("sites.columns.site"),
      dataIndex: "name",
      render: (v: string) => <Text strong>{v}</Text>,
    },
    {
      title: t("sites.columns.users"),
      dataIndex: "userCount",
      responsive: ["md"],
    },
    {
      title: t("sites.columns.usedQuota"),
      dataIndex: "totalUsedQuotaSize",
      responsive: ["md"],
      render: (v: number) => formatBytes(v),
    },
    {
      title: t("sites.columns.actions"),
      width: 200,
      render: (_: unknown, record) => (
        <Button
          icon={<PlusOutlined />}
          onClick={() => openCreate(record.name)}
          disabled={isPending}
        >
          {t("actions.addUser")}
        </Button>
      ),
    },
  ];

  const userColumns = useMemo(
    () =>
      getUserColumns({
        domain: config?.domain,
        port: config?.port,
        onEdit: openEdit,
        onResetPassword: openReset,
        onToggleEnabled: handleToggleEnabled,
        onDelete: handleDelete,
        onCopyConnection: handleCopyConnection,
        disableActions: isPending,
        t,
        dateLocale,
      }),
    [
      config,
      openEdit,
      openReset,
      handleToggleEnabled,
      handleDelete,
      handleCopyConnection,
      isPending,
      t,
      dateLocale,
    ],
  );

  return (
    <>
      <Flex gap="middle" vertical>
        <Flex justify="space-between" align="center">
          <PanelTitle>{t("title")}</PanelTitle>
          <Button
            type="primary"
            icon={<PlusOutlined />}
            onClick={() => openCreate()}
          >
            {t("actions.newUser")}
          </Button>
        </Flex>

        {config?.enabled && (config.domain || config.webUserInterface) && (
          <Descriptions
            bordered
            size="small"
            column={1}
            items={[
              config.domain
                ? {
                    key: "host",
                    label: t("connection.sftp"),
                    children: (
                      <Text code>
                        {config.domain}
                        {config.port ? `:${config.port}` : ""}
                      </Text>
                    ),
                  }
                : null,
              config.webUserInterface
                ? {
                    key: "web",
                    label: t("connection.webInterface"),
                    children: (
                      <Typography.Link
                        href={config.webUserInterface}
                        target="_blank"
                        rel="noreferrer"
                      >
                        {config.webUserInterface}
                      </Typography.Link>
                    ),
                  }
                : null,
            ].filter((x) => x !== null)}
          />
        )}

        <Table<SftpSiteModel>
          columns={siteColumns}
          dataSource={sites}
          loading={loadingSites || loadingUsers}
          rowKey="name"
          scroll={{ x: "max-content" }}
          locale={{ emptyText: t("sites.empty") }}
          pagination={false}
          expandable={{
            expandedRowRender: (record) => (
              <Table<SftpUserModel>
                columns={userColumns}
                dataSource={usersBySite.get(record.name) ?? []}
                rowKey="username"
                pagination={false}
                size="small"
                scroll={{ x: "max-content" }}
              />
            ),
          }}
        />
      </Flex>

      <SftpUserFormModal
        open={formOpen}
        onClose={closeForm}
        onCreate={handleCreate}
        onUpdate={handleUpdate}
        loading={creating || updating}
        user={userToEdit}
        defaultSite={defaultSite}
        existingSites={siteNames}
        initDirs={config?.initDirs}
      />

      <Modal
        title={t("resetPassword.title", {
          username: resetTarget?.username ?? "",
        })}
        open={resetTarget !== null}
        onOk={handleReset}
        onCancel={() => setResetTarget(null)}
        confirmLoading={resetting}
        okText={t("actions.reset")}
        cancelText={t("actions.cancel", { ns: "common" })}
        destroyOnHidden
      >
        <Form form={resetForm} layout="vertical">
          <Form.Item
            name="password"
            label={t("resetPassword.newPassword")}
            rules={[
              { required: true, message: t("validation.passwordRequired") },
              {
                min: 8,
                message: t("validation.passwordMinLength", { min: 8 }),
              },
            ]}
          >
            <Input.Password autoComplete="new-password" />
          </Form.Item>
        </Form>
      </Modal>
    </>
  );
}
