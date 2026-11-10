import { Alert, Checkbox, Flex, Input, Modal, Typography } from "antd";
import { useState } from "react";
import { Trans, useTranslation } from "react-i18next";

const { Paragraph, Text } = Typography;

interface Props {
  open: boolean;
  name: string;
  site: string;
  /** absolute path of the site directory, shown in the purge option */
  sitePath: string;
  loading: boolean;
  onConfirm: (purgeData: boolean) => void;
  onClose: () => void;
}

/**
 * Deletion of a stack. Site files are kept by default; purging them requires
 * typing the stack name, as SFTP users of the site lose their files.
 */
export default function DeleteStackModal({
  open,
  name,
  site,
  sitePath,
  loading,
  onConfirm,
  onClose,
}: Readonly<Props>) {
  const { t } = useTranslation("hosting");
  const [purge, setPurge] = useState(false);
  const [typed, setTyped] = useState("");
  const confirmed = !purge || typed.trim() === name;

  const reset = () => {
    setPurge(false);
    setTyped("");
  };

  return (
    <Modal
      title={t("delete.title", { name })}
      open={open}
      onOk={() => onConfirm(purge)}
      onCancel={onClose}
      afterOpenChange={(visible) => !visible && reset()}
      confirmLoading={loading}
      okText={t("delete.submit")}
      okButtonProps={{ danger: true, disabled: !confirmed }}
      cancelText={t("actions.cancel", { ns: "common" })}
    >
      <Flex vertical gap="middle">
        <Paragraph style={{ margin: 0 }}>{t("delete.description")}</Paragraph>
        <Checkbox checked={purge} onChange={(e) => setPurge(e.target.checked)}>
          {t("delete.purge", { path: sitePath })}
        </Checkbox>
        {purge && (
          <>
            <Alert
              type="error"
              showIcon
              title={t("delete.purgeWarning", { site })}
            />
            <Flex vertical gap={4}>
              <Text>
                <Trans
                  t={t}
                  i18nKey="delete.typeName"
                  values={{ name }}
                  components={{ code: <Text code /> }}
                />
              </Text>
              <Input
                value={typed}
                onChange={(e) => setTyped(e.target.value)}
                status={typed && !confirmed ? "error" : undefined}
                autoComplete="off"
                aria-label={t("delete.typeName", { name })}
              />
              {typed && !confirmed && (
                <Text type="danger">{t("delete.typeNameMismatch")}</Text>
              )}
            </Flex>
          </>
        )}
      </Flex>
    </Modal>
  );
}
