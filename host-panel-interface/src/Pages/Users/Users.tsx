import {
  EditFilled,
  ExclamationOutlined,
  FireOutlined,
  UserSwitchOutlined,
} from "@ant-design/icons";
import { Button, Flex, Modal, Space, Tooltip } from "antd";
import Table, { ColumnsType } from "antd/es/table";
import type { TFunction } from "i18next";
import { useCallback, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { PanelTitle } from "../../Components/Base/PanelTitle";
import UsersSidePanel from "../../Components/UsersSidePanel/UsersSidePanel";
import useDeleteUser from "../../Services/Users/Hooks/useDeleteUser";
import useMe from "../../Services/Users/Hooks/useMe";
import useUserAction from "../../Services/Users/Hooks/useUserAction";
import useUsers from "../../Services/Users/Hooks/useUsers";
import {
  UserActionEnum,
  UserModel,
  UserProfile,
} from "../../Services/Users/Models/users.model";

function profileLabel(t: TFunction<"users">, profile: UserProfile): string {
  switch (profile) {
    case UserProfile.ADMIN:
      return t("profiles.admin");
    case UserProfile.RESTRICTED:
      return t("profiles.restricted");
    default:
      return profile;
  }
}

function getColumns(params: {
  t: TFunction<"users">;
  switchProfile?: (email: string) => void;
  restrict?: (email: string) => void;
  edit?: (email: string) => void;
  deleteUser?: (email: string) => void;
  disableActions: boolean;
  myUserEmail: string;
}): ColumnsType<UserModel> {
  const { t } = params;
  return [
    {
      title: t("table.columns.email"),
      dataIndex: "email",
    },
    {
      title: t("table.columns.name"),
      dataIndex: "name",
      responsive: ["md"],
    },
    {
      title: t("table.columns.type"),
      dataIndex: "profile",
      responsive: ["md"],
      render: (value: UserProfile) => profileLabel(t, value),
    },
    {
      title: t("table.columns.permissions"),
      dataIndex: "permissions",
      responsive: ["md"],
      render: (values: string[], record) => {
        if (record.profile === UserProfile.ADMIN) {
          return t("table.permissions.allContainers");
        }

        if (values.length === 0) {
          return t("table.permissions.none");
        }

        if (values.length > 2) {
          return t("table.permissions.more", {
            list: values.slice(0, 2).join(", "),
            extra: values.length - 2,
          });
        }

        return values.join(", ");
      },
    },
    {
      title: t("table.columns.actions"),
      render: (_value, record: UserModel) => {
        if (record.email === params.myUserEmail) {
          return <span>{t("currentUser")}</span>;
        }

        return (
          <Space.Compact>
            <Tooltip title={t("actions.edit", { ns: "common" })}>
              <Button
                disabled={params.disableActions}
                shape="circle"
                type="primary"
                variant="outlined"
                onClick={() => params.edit?.(record.email)}
                icon={<EditFilled />}
              />
            </Tooltip>
            <Tooltip
              title={
                record.profile === UserProfile.ADMIN
                  ? t("actions.makeRestricted")
                  : t("actions.makeAdmin")
              }
            >
              <Button
                disabled={params.disableActions}
                shape="circle"
                type="primary"
                onClick={() => params.switchProfile?.(record.email)}
                icon={<UserSwitchOutlined />}
              />
            </Tooltip>

            <Tooltip title={t("actions.restrictAll")}>
              <Button
                danger
                disabled={params.disableActions}
                shape="circle"
                onClick={() => params.restrict?.(record.email)}
                icon={<ExclamationOutlined />}
              />
            </Tooltip>
            <Tooltip title={t("actions.delete", { ns: "common" })}>
              <Button
                danger
                disabled={params.disableActions}
                shape="circle"
                onClick={() => params.deleteUser?.(record.email)}
                icon={<FireOutlined />}
              />
            </Tooltip>
          </Space.Compact>
        );
      },
    },
  ];
}

export default function Users() {
  const { t } = useTranslation("users");
  const { data, isLoading } = useUsers();
  const { data: me } = useMe();

  const { mutate: doAction, isPending: pendingAction } = useUserAction();
  const { mutate: deleteUserAPI, isPending: pendingUserDelete } =
    useDeleteUser();

  const isPending = pendingAction || pendingUserDelete;

  const [userToEdit, setUserToEdit] = useState<UserModel | null>(null);

  const switchProfile = useCallback(
    (email: string) => {
      const user = data?.find((el) => el.email === email);
      if (!user) return;
      const isAdmin = user.profile === UserProfile.ADMIN;
      Modal.confirm({
        title: isAdmin ? t("modals.demote.title") : t("modals.promote.title"),
        content: isAdmin
          ? t("modals.demote.content", { email })
          : t("modals.promote.content", { email }),
        okText: isAdmin ? t("modals.demote.ok") : t("modals.promote.ok"),
        okButtonProps: { danger: isAdmin },
        cancelText: t("actions.cancel", { ns: "common" }),
        onOk: () => doAction({ email, action: UserActionEnum.SWITCH }),
      });
    },
    [data, doAction, t],
  );

  const edit = useCallback(
    (email: string) => {
      const user = data?.find((el) => el.email === email);
      if (!user) {
        return;
      }
      setUserToEdit(user);
    },
    [data],
  );

  const restrict = useCallback(
    (email: string) => {
      Modal.confirm({
        title: t("modals.restrict.title"),
        content: t("modals.restrict.content", { email }),
        okText: t("modals.restrict.ok"),
        okButtonProps: { danger: true },
        cancelText: t("actions.cancel", { ns: "common" }),
        onOk: () => doAction({ email, action: UserActionEnum.RESTRICT }),
      });
    },
    [doAction, t],
  );

  const deleteUser = useCallback(
    (email: string) => {
      Modal.confirm({
        title: t("modals.delete.title"),
        content: t("modals.delete.content", { email }),
        okText: t("actions.delete", { ns: "common" }),
        okButtonProps: { danger: true },
        cancelText: t("actions.cancel", { ns: "common" }),
        onOk: () => deleteUserAPI({ email }),
      });
    },
    [deleteUserAPI, t],
  );

  const myUserEmail = me?.email;
  const columns = useMemo(
    () =>
      getColumns({
        t,
        switchProfile,
        edit,
        restrict,
        deleteUser,
        disableActions: isPending,
        myUserEmail: myUserEmail ?? "",
      }),
    [t, switchProfile, edit, restrict, deleteUser, isPending, myUserEmail],
  );

  if (!me) return <></>;

  return (
    <>
      <Flex gap="middle" vertical>
        <PanelTitle>{t("title")}</PanelTitle>

        <Table
          columns={columns}
          dataSource={data}
          loading={isLoading}
          rowKey="email"
          scroll={{ x: "max-content" }}
        />
      </Flex>

      <UsersSidePanel onClose={() => setUserToEdit(null)} user={userToEdit} />
    </>
  );
}
