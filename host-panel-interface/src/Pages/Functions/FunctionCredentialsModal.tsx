import { EyeInvisibleOutlined, EyeOutlined } from "@ant-design/icons";
import { Alert, Button, Descriptions, Modal, Space, Typography } from "antd";
import { ReactNode, useState } from "react";
import { useTranslation } from "react-i18next";
import { FunctionCredentialsModel } from "../../Services/Functions/Models/functions.model";

const { Text, Paragraph } = Typography;

/** A value, masked until revealed when it is a secret (copy works anyway). */
function Value({
  value,
  secret = false,
}: Readonly<{ value: string; secret?: boolean }>) {
  const { t } = useTranslation("functions");
  const [revealed, setRevealed] = useState(!secret);
  return (
    <Space size={4} wrap>
      <Text code copyable={{ text: value }} style={{ wordBreak: "break-all" }}>
        {revealed ? value : "••••••••••••"}
      </Text>
      {secret && (
        <Button
          type="link"
          size="small"
          icon={revealed ? <EyeInvisibleOutlined /> : <EyeOutlined />}
          onClick={() => setRevealed((r) => !r)}
        >
          {revealed ? t("credentials.hide") : t("credentials.show")}
        </Button>
      )}
    </Space>
  );
}

interface Props {
  /** null: closed */
  credentials: FunctionCredentialsModel | null;
  onClose: () => void;
  /** e.g. the progress of the steps that follow a creation */
  children?: ReactNode;
  /** the close button waits for these steps */
  closeDisabled?: boolean;
}

/**
 * Shows the invoke credentials once: the panel keeps only their hash (the
 * bearer token) or puts them in the container (SigV4), it cannot show them
 * again.
 */
export default function FunctionCredentialsModal({
  credentials,
  onClose,
  children,
  closeDisabled = false,
}: Readonly<Props>) {
  const { t } = useTranslation("functions");
  const curl = credentials
    ? `curl -sS -X POST -H "Authorization: Bearer ${credentials.token}" -H "Content-Type: application/json" -d '{}' '${credentials.endpoint}'`
    : "";
  // Unmounted once closed: the credentials leave the DOM at once (a
  // closing antd modal keeps its last content while it animates).
  if (!credentials) return null;
  return (
    <Modal
      title={t("credentials.title")}
      open
      onCancel={onClose}
      // Shown once: no accidental close (mask, Escape).
      maskClosable={false}
      keyboard={false}
      closable={!closeDisabled}
      width={680}
      destroyOnHidden
      footer={
        <Button type="primary" disabled={closeDisabled} onClick={onClose}>
          {t("credentials.close")}
        </Button>
      }
    >
      <Alert
        type="warning"
        showIcon
        title={t("credentials.warning")}
        style={{ marginBottom: 16 }}
      />
      {credentials && (
        <Descriptions
          bordered
          size="small"
          column={1}
          items={[
            {
              key: "endpoint",
              label: t("credentials.fields.endpoint"),
              children: <Value value={credentials.endpoint} />,
            },
            {
              key: "region",
              label: t("credentials.fields.region"),
              children: <Value value={credentials.region} />,
            },
            {
              key: "token",
              label: t("credentials.fields.token"),
              children: <Value value={credentials.token} secret />,
            },
            ...(credentials.accessKeyId && credentials.secretAccessKey
              ? [
                  {
                    key: "accessKeyId",
                    label: t("credentials.fields.accessKeyId"),
                    children: <Value value={credentials.accessKeyId} />,
                  },
                  {
                    key: "secretAccessKey",
                    label: t("credentials.fields.secretAccessKey"),
                    children: (
                      <Value value={credentials.secretAccessKey} secret />
                    ),
                  },
                ]
              : []),
          ]}
        />
      )}
      {credentials?.secretAccessKey && (
        <Alert
          type="info"
          showIcon
          title={t("credentials.sigv4Note")}
          style={{ marginTop: 12 }}
        />
      )}
      <Paragraph
        copyable={{ text: curl }}
        style={{ marginTop: 12, marginBottom: 0 }}
      >
        {t("credentials.curl")}
      </Paragraph>
      <Paragraph type="secondary" style={{ marginTop: 8, marginBottom: 0 }}>
        {t("credentials.internalOnly")}
      </Paragraph>
      {children}
    </Modal>
  );
}
