import { Alert, Flex, Form, Modal, Typography } from "antd";
import { useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { toast } from "react-toastify";
import { useInstanceNetworksUpdate } from "../../Services/Databases/Hooks/useDatabaseMutations";
import { ServiceInstanceModel } from "../../Services/Databases/Models/databases.model";
import useNetworks from "../../Services/Networks/Hooks/useNetworks";
import { SYSTEM_NETWORK } from "../../Services/Networks/Models/networks.model";
import { getApiErrorMessage } from "../../Utils/apiErrors";
import NetworkSelect from "../Networks/NetworkSelect";

interface Props {
  /** null: closed */
  instance: ServiceInstanceModel | null;
  onClose: () => void;
}

/** Same comparison as the API (registry names, order irrelevant). */
const sameSet = (a: readonly string[], b: readonly string[]) =>
  a.length === b.length && a.every((x) => b.includes(x));

function NetworksForm({
  instance,
  onClose,
}: Readonly<{ instance: ServiceInstanceModel; onClose: () => void }>) {
  const { t } = useTranslation("databases");
  const { data: networks, isLoading } = useNetworks();
  const { mutateAsync, isPending } = useInstanceNetworksUpdate();
  const [value, setValue] = useState<string[]>(instance.networks ?? []);
  const [touched, setTouched] = useState(false);
  const unauthenticated = !!instance.unauthenticated;
  const disabledReasons = useMemo(
    () =>
      unauthenticated
        ? { [SYSTEM_NETWORK]: t("networks.unauthenticatedShared") }
        : undefined,
    [unauthenticated, t],
  );
  const empty = value.length === 0;
  const sharedRefused = unauthenticated && value.includes(SYSTEM_NETWORK);
  const error = empty
    ? t("networks.required")
    : sharedRefused
      ? t("networks.unauthenticatedShared")
      : undefined;
  const unchanged = sameSet(value, instance.networks ?? []);

  const save = async () => {
    setTouched(true);
    if (error || unchanged || isPending) return;
    try {
      await mutateAsync({ instance: instance.key, networks: value });
      toast.success(t("networks.saved", { name: instance.project }));
      onClose();
    } catch (err) {
      toast.error(getApiErrorMessage(err));
    }
  };

  return (
    <Modal
      open
      title={t("networks.modalTitle", { name: instance.project })}
      okText={t("actions.save", { ns: "common" })}
      cancelText={t("actions.cancel", { ns: "common" })}
      okButtonProps={{ loading: isPending, disabled: unchanged }}
      onOk={() => void save()}
      onCancel={onClose}
      destroyOnHidden
    >
      <Flex vertical gap="middle">
        <Typography.Text type="secondary">{t("networks.help")}</Typography.Text>
        <Form layout="vertical">
          <Form.Item
            label={t("networks.label")}
            htmlFor="instance-networks"
            validateStatus={touched && error ? "error" : undefined}
            help={touched ? error : undefined}
            style={{ marginBottom: 0 }}
          >
            <NetworkSelect
              id="instance-networks"
              aria-label={t("networks.label")}
              value={value}
              onChange={(v) => {
                setTouched(true);
                setValue(v);
              }}
              networks={networks}
              loading={isLoading}
              disabledReasons={disabledReasons}
              showCreateLink
              status={touched && error ? "error" : undefined}
            />
          </Form.Item>
        </Form>
        <Alert
          type="warning"
          showIcon
          message={t("networks.restart", { container: instance.project })}
        />
      </Flex>
    </Modal>
  );
}

/** Changes the networks of an instance (it is recreated on them). */
export default function InstanceNetworksModal({
  instance,
  onClose,
}: Readonly<Props>) {
  // Mounted per instance: the form starts from its current networks.
  return instance ? (
    <NetworksForm key={instance.key} instance={instance} onClose={onClose} />
  ) : null;
}
