import { Button, Drawer, Flex, Transfer } from "antd";
import { useCallback, useEffect, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import useContainers from "../../Services/Containers/Hooks/useContainers";
import useUpdateUserACL from "../../Services/Users/Hooks/useUpdateUserACL";
import { UserModel } from "../../Services/Users/Models/users.model";

interface Props {
  onClose?: () => void;
  user: UserModel | null;
}

export default function UsersSidePanel({ user, onClose }: Readonly<Props>) {
  const { t } = useTranslation("users");
  const { mutate: updateUserACL, isPending: pendingUpdateACL } =
    useUpdateUserACL();
  const { data: containers, isLoading } = useContainers();

  const [targetKeys, setTargetKeys] = useState<string[]>([]);

  useEffect(() => {
    if (user) {
      setTargetKeys(user.permissions);
    }
  }, [user]);

  const containersPicker = useMemo(() => {
    if (!containers) return [];

    return containers.map((el) => ({
      key: el.Names[0].substring(1),
      title: el.Names[0].substring(1),
    }));
  }, [containers]);

  const handleClose = useCallback(() => {
    setTargetKeys([]);
    onClose?.();
  }, [onClose]);

  const handleValidate = useCallback(() => {
    updateUserACL(
      {
        email: user?.email ?? "",
        permissions: targetKeys,
      },
      {
        onSuccess: handleClose,
      },
    );
  }, [handleClose, targetKeys, updateUserACL, user]);

  return (
    <Drawer
      closable
      destroyOnHidden
      open={user !== null}
      onClose={handleClose}
      placement="right"
      title={t("sidePanel.title", { name: user?.name ?? "" })}
      size="100vw"
      loading={isLoading}
    >
      <Flex vertical gap="middle">
        <Transfer
          dataSource={containersPicker}
          showSearch
          searchPlaceholder={t("sidePanel.searchPlaceholder")}
          filterOption={(input, item) =>
            (item?.title ?? "").toLowerCase().includes(input.toLowerCase())
          }
          styles={{ section: { width: "100%", height: "80vh" } }}
          targetKeys={targetKeys}
          render={renderItem}
          // @ts-expect-error bad typing
          onChange={(keys: string[]) => setTargetKeys(keys)}
        />
        <Button
          type="primary"
          onClick={handleValidate}
          loading={pendingUpdateACL}
        >
          {t("sidePanel.validate")}
        </Button>
        <Button type="default" onClick={handleClose}>
          {t("actions.close", { ns: "common" })}
        </Button>
      </Flex>
    </Drawer>
  );
}

const renderItem = (item: { key: string; title: string }) => {
  const customLabel = <span className="custom-item">{item.title}</span>;

  return {
    label: customLabel, // for displayed item
    value: item.title, // for title and filter matching
  };
};
