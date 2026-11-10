import { Alert, Input, Modal, Space, Typography } from "antd";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { ResourceWording } from "../../Services/Databases/Models/databases.model";

interface Props {
  target: {
    name: string;
    last: boolean;
    engineName: string;
    version: string;
    wording: ResourceWording;
  } | null;
  loading: boolean;
  onCancel: () => void;
  onConfirm: () => void;
}

/** Deleting a database is permanent: the name must be typed again. */
export default function DeleteDatabaseModal({
  target,
  loading,
  onCancel,
  onConfirm,
}: Readonly<Props>) {
  const { t } = useTranslation("databases");
  const [typed, setTyped] = useState("");
  const close = () => {
    setTyped("");
    onCancel();
  };
  return (
    <Modal
      title={t(`wording.${target?.wording ?? "database"}.deleteTitle`, {
        name: target?.name ?? "",
      })}
      open={target !== null}
      onCancel={close}
      onOk={onConfirm}
      okText={t("actions.delete", { ns: "common" })}
      okButtonProps={{
        danger: true,
        disabled: !target?.name || typed !== target.name,
      }}
      cancelText={t("actions.cancel", { ns: "common" })}
      confirmLoading={loading}
      afterClose={() => setTyped("")}
      destroyOnHidden
    >
      <Space direction="vertical" style={{ width: "100%" }}>
        <Typography.Paragraph style={{ marginBottom: 0 }}>
          {t(`wording.${target?.wording ?? "database"}.deleteContent`)}
        </Typography.Paragraph>
        {target?.last && (
          <Alert
            type="warning"
            showIcon
            message={t(`wording.${target.wording}.deleteLast`, {
              engine: target.engineName,
              version: target.version,
            })}
          />
        )}
        <Typography.Text>
          {t("delete.confirmLabel", { name: target?.name ?? "" })}
        </Typography.Text>
        <Input
          value={typed}
          onChange={(e) => setTyped(e.target.value)}
          autoComplete="off"
          spellCheck={false}
        />
      </Space>
    </Modal>
  );
}
