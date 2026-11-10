import { EyeInvisibleOutlined, EyeOutlined } from "@ant-design/icons";
import { Alert, Button, Descriptions, Modal, Space, Typography } from "antd";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { CredentialModel } from "../../Services/Databases/Models/databases.model";

const FIELD_KEYS = [
  "host",
  "port",
  "database",
  "user",
  "password",
  "uri",
  "external",
  "tlsFingerprint",
  "vhost",
  "endpoint",
  "region",
  "bucket",
  "accessKeyId",
  "secretAccessKey",
  "pathStyle",
  "queueUrlPrefix",
] as const;

/** The .env lines of a Laravel S3 disk, when the credentials are S3 ones. */
function laravelEnv(credentials: CredentialModel[]): string | null {
  const get = (k: string) => credentials.find((c) => c.key === k)?.value;
  const endpoint = get("endpoint");
  const key = get("accessKeyId");
  const secret = get("secretAccessKey");
  if (!endpoint || !key || !secret) return null;
  const bucket = get("bucket");
  return [
    `AWS_ACCESS_KEY_ID=${key}`,
    `AWS_SECRET_ACCESS_KEY=${secret}`,
    `AWS_DEFAULT_REGION=${get("region") ?? "us-east-1"}`,
    // The resource bucket (created at start by the instance).
    ...(bucket ? [`AWS_BUCKET=${bucket}`] : []),
    `AWS_ENDPOINT=${endpoint}`,
    "AWS_USE_PATH_STYLE_ENDPOINT=true",
  ].join("\n");
}
type FieldKey = (typeof FIELD_KEYS)[number];

function isFieldKey(key: string): key is FieldKey {
  return (FIELD_KEYS as readonly string[]).includes(key);
}

/** A value, masked until revealed when it is a secret (copy works anyway). */
function CredentialValue({
  credential,
}: Readonly<{ credential: CredentialModel }>) {
  const { t } = useTranslation("databases");
  const [revealed, setRevealed] = useState(!credential.secret);
  return (
    <Space size={4} wrap>
      <Typography.Text
        code
        copyable={{ text: credential.value }}
        style={{ wordBreak: "break-all" }}
      >
        {revealed ? credential.value : "••••••••••••"}
      </Typography.Text>
      {credential.secret && (
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
  credentials: CredentialModel[] | null;
  onClose: () => void;
}

/** Shows the connection details once: the password cannot be read again. */
export default function CredentialsModal({
  credentials,
  onClose,
}: Readonly<Props>) {
  const { t } = useTranslation("databases");
  // ElasticMQ: no password, no authentication at all.
  const hasSecret = (credentials ?? []).some((c) => c.secret);
  return (
    <Modal
      title={t("credentials.title")}
      open={credentials !== null}
      onCancel={onClose}
      // The password is shown once: no accidental close (mask, Escape).
      maskClosable={false}
      keyboard={false}
      width={640}
      destroyOnHidden
      footer={
        <Button type="primary" onClick={onClose}>
          {hasSecret
            ? t("credentials.close")
            : t("actions.close", { ns: "common" })}
        </Button>
      }
    >
      <Alert
        type="warning"
        showIcon
        message={hasSecret ? t("credentials.warning") : t("credentials.noAuth")}
        style={{ marginBottom: 16 }}
      />
      <Descriptions
        bordered
        size="small"
        column={1}
        items={(credentials ?? []).map((c) => ({
          key: c.key,
          label: isFieldKey(c.key) ? t(`credentials.fields.${c.key}`) : c.key,
          children: <CredentialValue credential={c} />,
        }))}
      />
      {credentials && laravelEnv(credentials) && (
        <Typography.Paragraph
          copyable={{ text: laravelEnv(credentials) ?? "" }}
          style={{ marginTop: 12, marginBottom: 0 }}
        >
          {t("credentials.laravelEnv")}
        </Typography.Paragraph>
      )}
      {!credentials?.some((c) => c.key === "external") && (
        <Typography.Paragraph
          type="secondary"
          style={{ marginTop: 12, marginBottom: 0 }}
        >
          {t("credentials.internalOnly")}
        </Typography.Paragraph>
      )}
    </Modal>
  );
}
