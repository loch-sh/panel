import { Alert, Checkbox, Flex, Modal, Typography } from "antd";
import { useState } from "react";
import { useTranslation } from "react-i18next";

interface Props {
  open: boolean;
  name: string;
  /** compose file edited outside the panel: deploying it needs force=true */
  modified: boolean;
  loading: boolean;
  onConfirm: (pull: boolean) => void;
  onClose: () => void;
}

export default function RedeployModal({
  open,
  name,
  modified,
  loading,
  onConfirm,
  onClose,
}: Readonly<Props>) {
  const { t } = useTranslation("hosting");
  const [pull, setPull] = useState(false);
  const [acknowledged, setAcknowledged] = useState(false);

  const reset = () => {
    setPull(false);
    setAcknowledged(false);
  };

  return (
    <Modal
      title={t("redeploy.title", { name })}
      open={open}
      onOk={() => onConfirm(pull)}
      onCancel={onClose}
      afterOpenChange={(visible) => !visible && reset()}
      confirmLoading={loading}
      okText={t("redeploy.submit")}
      okButtonProps={{
        danger: modified,
        disabled: modified && !acknowledged,
      }}
      cancelText={t("actions.cancel", { ns: "common" })}
      destroyOnHidden
    >
      <Flex vertical gap="middle">
        <Typography.Paragraph style={{ margin: 0 }}>
          {t("redeploy.description")}
        </Typography.Paragraph>
        {modified && (
          <>
            <Alert
              type="warning"
              showIcon
              title={t("redeploy.modifiedWarning")}
            />
            <Checkbox
              checked={acknowledged}
              onChange={(e) => setAcknowledged(e.target.checked)}
            >
              {t("redeploy.modifiedConfirm")}
            </Checkbox>
          </>
        )}
        <Checkbox checked={pull} onChange={(e) => setPull(e.target.checked)}>
          {t("redeploy.pull")}
        </Checkbox>
      </Flex>
    </Modal>
  );
}
