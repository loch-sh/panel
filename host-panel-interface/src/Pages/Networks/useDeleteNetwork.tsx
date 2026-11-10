import { Modal } from "antd";
import { useCallback } from "react";
import { useTranslation } from "react-i18next";
import { toast } from "react-toastify";
import { useNetworkDelete } from "../../Services/Networks/Hooks/useNetworkMutations";
import { getApiErrorMessage } from "../../Utils/apiErrors";

/** Asks for confirmation, then deletes a network (409 while it has members). */
export default function useDeleteNetwork(onDeleted?: () => void) {
  const { t } = useTranslation("networks");
  const { mutateAsync, isPending } = useNetworkDelete();
  const ask = useCallback(
    (name: string) => {
      Modal.confirm({
        title: t("delete.title", { name }),
        content: t("delete.content"),
        okText: t("delete.submit"),
        okButtonProps: { danger: true },
        cancelText: t("actions.cancel", { ns: "common" }),
        onOk: async () => {
          try {
            await mutateAsync(name);
            toast.success(t("toasts.deleted", { name }));
            onDeleted?.();
          } catch (err) {
            toast.error(getApiErrorMessage(err));
          }
        },
      });
    },
    [mutateAsync, onDeleted, t],
  );
  return { ask, isPending };
}
