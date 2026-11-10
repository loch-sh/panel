import { Alert, Form, Input, Modal, Typography } from "antd";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { toast } from "react-toastify";
import { useInstancePurge } from "../../Services/Databases/Hooks/useDatabaseMutations";
import { ServiceInstanceModel } from "../../Services/Databases/Models/databases.model";
import { getApiErrorMessage } from "../../Utils/apiErrors";

interface Props {
  instance: ServiceInstanceModel | null;
  onClose: () => void;
}

/**
 * Deletes an instance edited by hand, with all its data: confirmed by typing
 * its name (nothing can be recovered, every database it held is lost).
 */
export default function PurgeInstanceModal({
  instance,
  onClose,
}: Readonly<Props>) {
  const { t } = useTranslation("databases");
  const [typed, setTyped] = useState("");
  const { mutateAsync: purge, isPending } = useInstancePurge();
  const name = instance?.project ?? "";

  const close = () => {
    setTyped("");
    onClose();
  };
  const submit = async () => {
    if (!instance || typed !== name) return;
    try {
      await purge(instance.key);
      toast.success(t("toasts.purged"));
      close();
    } catch (err) {
      toast.error(getApiErrorMessage(err));
    }
  };

  return (
    <Modal
      open={instance !== null}
      title={t("purge.title", { name })}
      okText={t("purge.submit")}
      okButtonProps={{ danger: true, disabled: typed !== name }}
      confirmLoading={isPending}
      cancelText={t("actions.cancel", { ns: "common" })}
      onOk={() => void submit()}
      onCancel={close}
      destroyOnHidden
    >
      <Alert
        type="error"
        showIcon
        message={t("purge.content")}
        style={{ marginBottom: 16 }}
      />
      <Form layout="vertical" onFinish={() => void submit()}>
        <Form.Item
          label={
            <Typography.Text>
              {t("purge.confirmLabel")}{" "}
              <Typography.Text code>{name}</Typography.Text>
            </Typography.Text>
          }
          style={{ marginBottom: 0 }}
        >
          <Input
            value={typed}
            onChange={(e) => setTyped(e.target.value)}
            autoComplete="off"
            spellCheck={false}
            aria-label={t("purge.confirmLabel")}
          />
        </Form.Item>
      </Form>
    </Modal>
  );
}
