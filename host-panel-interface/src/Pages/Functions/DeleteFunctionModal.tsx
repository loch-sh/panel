import { Alert, Form, Input, Modal, Typography } from "antd";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { toast } from "react-toastify";
import { useFunctionDelete } from "../../Services/Functions/Hooks/useFunctionMutations";
import { getApiErrorMessage } from "../../Utils/apiErrors";

export interface DeleteFunctionTarget {
  name: string;
  /** edited outside the panel (or unreadable): removed without running its files */
  purge: boolean;
}

interface Props {
  target: DeleteFunctionTarget | null;
  onClose: () => void;
  onDeleted?: (name: string) => void;
}

/**
 * Deletes a function (code, deployments, secrets, credentials and its own
 * network): confirmed by typing its name, nothing can be recovered.
 */
export default function DeleteFunctionModal({
  target,
  onClose,
  onDeleted,
}: Readonly<Props>) {
  const { t } = useTranslation("functions");
  const [typed, setTyped] = useState("");
  const { mutateAsync: remove, isPending } = useFunctionDelete();
  const name = target?.name ?? "";
  const purge = target?.purge ?? false;
  const mode = purge ? "purge" : "delete";

  const close = () => {
    setTyped("");
    onClose();
  };
  const submit = async () => {
    if (!target || typed !== name) return;
    try {
      const res = await remove({ name, purge });
      toast.success(t(`toasts.${purge ? "purged" : "deleted"}`, { name }));
      if (res?.networkKept) {
        toast.info(t("toasts.networkKept", { network: `fn-${name}` }));
      }
      setTyped("");
      onDeleted?.(name);
      onClose();
    } catch (err) {
      toast.error(getApiErrorMessage(err));
    }
  };

  return (
    <Modal
      open={target !== null}
      title={t(`${mode}.title`, { name })}
      okText={t(`${mode}.submit`)}
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
        title={t(`${mode}.content`)}
        style={{ marginBottom: 16 }}
      />
      <Form layout="vertical" onFinish={() => void submit()}>
        <Form.Item
          label={
            <Typography.Text>
              {t("delete.confirmLabel")}{" "}
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
            aria-label={t("delete.confirmLabel")}
          />
        </Form.Item>
      </Form>
    </Modal>
  );
}
