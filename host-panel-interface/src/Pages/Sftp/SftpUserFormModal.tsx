import {
  CopyOutlined,
  FolderOutlined,
  LockOutlined,
  ReloadOutlined,
  UserOutlined,
} from "@ant-design/icons";
import {
  Alert,
  AutoComplete,
  Button,
  Col,
  Divider,
  Form,
  Input,
  InputNumber,
  Modal,
  Row,
  Select,
  Space,
  Switch,
  Tooltip,
  Typography,
  message,
} from "antd";
import { useCallback, useEffect } from "react";
import { Trans, useTranslation } from "react-i18next";
import {
  SftpUserCreatePayload,
  SftpUserModel,
  SftpUserUpdatePayload,
} from "../../Services/Sftp/Models/sftp.model";
import { ByteUnit, toBytes } from "../../Utils/bytes";

const { Text } = Typography;

interface FormValues {
  site: string;
  subPath?: string;
  username: string;
  password?: string;
  publicKey?: string;
  quotaSize?: number;
  quotaUnit: ByteUnit;
  quotaFiles?: number;
  bandwidthKbps?: number;
  requirePasswordChange: boolean;
  initializeDirs: boolean;
}

interface Props {
  open: boolean;
  onClose: () => void;
  onCreate: (payload: SftpUserCreatePayload) => Promise<void>;
  onUpdate: (username: string, payload: SftpUserUpdatePayload) => Promise<void>;
  loading: boolean;
  /** null = create mode, otherwise edit mode */
  user: SftpUserModel | null;
  /** site name pre-selected when creating from a site row */
  defaultSite?: string;
  existingSites: string[];
  /** directories proposed for initialization (from the SFTP config) */
  initDirs?: string[];
}

/** Reasons a sub-path is rejected; mapped to translated messages in the UI. */
type SubPathErrorCode = "absolute" | "emptySegment" | "dotSegment" | "invalidChars";

/**
 * Validates a sub-path entered by the admin, mirroring the server-side rules.
 * Returns an error code (translated by the caller) or null when valid. The
 * server stays the source of truth; this only improves the UX. A leading dot
 * is allowed so hidden directories such as ".well-known" remain valid, but the
 * "." and ".." references are rejected to prevent escaping the site directory.
 */
function subPathError(value?: string): SubPathErrorCode | null {
  const v = (value ?? "").trim();
  if (!v) return null;
  if (v.startsWith("/")) {
    return "absolute";
  }
  for (const segment of v.split("/")) {
    if (segment === "") {
      return "emptySegment";
    }
    if (segment === "." || segment === "..") {
      return "dotSegment";
    }
    for (const ch of segment) {
      const code = ch.codePointAt(0) ?? 0;
      if (ch === "\\" || code < 0x20 || code === 0x7f) {
        return "invalidChars";
      }
    }
  }
  return null;
}

/** Generates a strong random password using the Web Crypto API. */
function generatePassword(length = 20): string {
  const charset =
    "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789!@#$%^&*-_";
  const values = new Uint32Array(length);
  crypto.getRandomValues(values);
  return Array.from(values, (v) => charset[v % charset.length]).join("");
}

export default function SftpUserFormModal({
  open,
  onClose,
  onCreate,
  onUpdate,
  loading,
  user,
  defaultSite,
  existingSites,
  initDirs,
}: Readonly<Props>) {
  const { t } = useTranslation("sftp");
  const [form] = Form.useForm<FormValues>();
  const isEdit = user !== null;
  const canInitDirs = !isEdit && !!initDirs && initDirs.length > 0;
  const passwordValue = Form.useWatch("password", form);
  const subPathValue = Form.useWatch("subPath", form);
  const subPathErrorMessage = useCallback(
    (code: SubPathErrorCode): string => {
      switch (code) {
        case "absolute":
          return t("validation.subPath.absolute");
        case "emptySegment":
          return t("validation.subPath.emptySegment");
        case "dotSegment":
          return t("validation.subPath.dotSegment");
        case "invalidChars":
          return t("validation.subPath.invalidChars");
      }
    },
    [t]
  );
  const subPathErrorCode = isEdit ? null : subPathError(subPathValue);

  useEffect(() => {
    if (!open) return;
    if (user) {
      form.setFieldsValue({
        site: user.site,
        subPath: user.subPath,
        username: user.username,
        publicKey: user.publicKeys[0] ?? "",
        quotaSize: user.quotaSize ? user.quotaSize / 1024 ** 3 : undefined,
        quotaUnit: "GB",
        quotaFiles: user.quotaFiles || undefined,
        bandwidthKbps: user.uploadBandwidth || undefined,
        requirePasswordChange: user.requirePasswordChange,
      });
    } else {
      form.resetFields();
      form.setFieldsValue({
        site: defaultSite ?? "",
        quotaUnit: "GB",
        requirePasswordChange: false,
        initializeDirs: true,
      });
    }
  }, [open, user, defaultSite, form]);

  const handleGenerate = useCallback(() => {
    form.setFieldValue("password", generatePassword());
    form.validateFields(["password"]);
  }, [form]);

  const handleCopy = useCallback(() => {
    const pw = form.getFieldValue("password");
    if (!pw) return;
    navigator.clipboard.writeText(pw);
    message.success(t("toasts.passwordCopied"));
  }, [form, t]);

  const handleOk = useCallback(async () => {
    const values = await form.validateFields();
    const quotaSizeBytes = values.quotaSize
      ? toBytes(values.quotaSize, values.quotaUnit)
      : 0;

    if (isEdit && user) {
      await onUpdate(user.username, {
        publicKey: values.publicKey?.trim() || undefined,
        quotaSizeBytes,
        quotaFiles: values.quotaFiles ?? 0,
        bandwidthKbps: values.bandwidthKbps ?? 0,
        requirePasswordChange: values.requirePasswordChange,
      });
    } else {
      await onCreate({
        username: values.username.trim(),
        site: values.site.trim(),
        subPath: values.subPath?.trim() || undefined,
        password: values.password ?? "",
        publicKey: values.publicKey?.trim() || undefined,
        quotaSizeBytes,
        quotaFiles: values.quotaFiles ?? 0,
        bandwidthKbps: values.bandwidthKbps ?? 0,
        requirePasswordChange: values.requirePasswordChange,
        initializeDirs: canInitDirs ? values.initializeDirs : false,
      });
    }
  }, [form, isEdit, user, onCreate, onUpdate, canInitDirs]);

  return (
    <Modal
      title={
        isEdit
          ? t("form.title.edit", { username: user?.username ?? "" })
          : t("form.title.create")
      }
      open={open}
      onOk={handleOk}
      onCancel={onClose}
      confirmLoading={loading}
      okText={
        isEdit
          ? t("actions.save", { ns: "common" })
          : t("form.submit.create")
      }
      cancelText={t("actions.cancel", { ns: "common" })}
      width={540}
      destroyOnHidden
    >
      <Form form={form} layout="vertical" requiredMark="optional">
        <Divider titlePlacement="start" orientationMargin={0} plain>
          {t("form.sections.identity")}
        </Divider>

        <Form.Item
          name="site"
          label={t("form.fields.site.label")}
          rules={[{ required: true, message: t("validation.siteRequired") }]}
          tooltip={!isEdit ? t("form.fields.site.tooltip") : undefined}
        >
          <AutoComplete
            disabled={isEdit}
            options={existingSites.map((s) => ({ value: s }))}
            showSearch={{
              filterOption: (input, option) =>
                (option?.value ?? "")
                  .toString()
                  .toLowerCase()
                  .includes(input.toLowerCase()),
            }}
          >
            <Input
              prefix={<FolderOutlined />}
              placeholder={t("form.fields.site.placeholder")}
              allowClear
            />
          </AutoComplete>
        </Form.Item>

        <Form.Item
          name="subPath"
          label={t("form.fields.subPath.label")}
          tooltip={
            isEdit
              ? t("form.fields.subPath.tooltipEdit")
              : t("form.fields.subPath.tooltipCreate")
          }
          rules={[
            {
              validator: (_, value?: string) => {
                const code = subPathError(value);
                return code
                  ? Promise.reject(new Error(subPathErrorMessage(code)))
                  : Promise.resolve();
              },
            },
          ]}
        >
          <Input
            prefix={<FolderOutlined />}
            disabled={isEdit}
            placeholder={t("form.fields.subPath.placeholder")}
            allowClear
            autoComplete="off"
          />
        </Form.Item>

        {subPathErrorCode && (
          <Alert
            type="error"
            showIcon
            title={subPathErrorMessage(subPathErrorCode)}
            style={{ marginBottom: 16 }}
          />
        )}

        <Form.Item
          name="username"
          label={t("form.fields.username.label")}
          rules={[
            { required: true, message: t("validation.usernameRequired") },
          ]}
        >
          <Input
            prefix={<UserOutlined />}
            disabled={isEdit}
            placeholder={t("form.fields.username.placeholder")}
            autoComplete="off"
          />
        </Form.Item>

        {!isEdit && (
          <Form.Item label={t("form.fields.password.label")} required>
            <Space.Compact style={{ width: "100%" }}>
              <Form.Item
                name="password"
                noStyle
                rules={[
                  { required: true, message: t("validation.passwordRequired") },
                  {
                    min: 8,
                    message: t("validation.passwordMinLength", { min: 8 }),
                  },
                ]}
              >
                <Input.Password
                  prefix={<LockOutlined />}
                  placeholder={t("form.fields.password.placeholder")}
                  autoComplete="new-password"
                />
              </Form.Item>
              <Tooltip title={t("form.fields.password.generateTooltip")}>
                <Button icon={<ReloadOutlined />} onClick={handleGenerate}>
                  {t("form.fields.password.generate")}
                </Button>
              </Tooltip>
              <Tooltip title={t("form.fields.password.copyTooltip")}>
                <Button
                  icon={<CopyOutlined />}
                  onClick={handleCopy}
                  disabled={!passwordValue}
                />
              </Tooltip>
            </Space.Compact>
          </Form.Item>
        )}

        <Form.Item
          name="publicKey"
          label={t("form.fields.publicKey.label")}
          tooltip={t("form.fields.publicKey.tooltip")}
        >
          <Input.TextArea
            rows={3}
            placeholder="ssh-ed25519 AAAA..."
            autoComplete="off"
            style={{ fontFamily: "monospace", fontSize: 12 }}
          />
        </Form.Item>

        <Divider titlePlacement="start" orientationMargin={0} plain>
          {t("form.sections.quotas")}
        </Divider>

        <Row gutter={12}>
          <Col span={12}>
            <Form.Item
              label={t("form.fields.quotaSize.label")}
              tooltip={t("form.fields.quotaSize.tooltip")}
            >
              <Space.Compact style={{ width: "100%" }}>
                <Form.Item name="quotaSize" noStyle>
                  <InputNumber
                    min={0}
                    style={{ width: "100%" }}
                    placeholder={t("form.fields.quotaSize.placeholder")}
                  />
                </Form.Item>
                <Form.Item name="quotaUnit" noStyle>
                  <Select
                    style={{ width: 80 }}
                    options={[
                      { value: "GB", label: t("units.gb") },
                      { value: "MB", label: t("units.mb") },
                    ]}
                  />
                </Form.Item>
              </Space.Compact>
            </Form.Item>
          </Col>
          <Col span={12}>
            <Form.Item
              name="quotaFiles"
              label={t("form.fields.quotaFiles.label")}
              tooltip={t("form.fields.quotaFiles.tooltip")}
            >
              <InputNumber
                min={0}
                style={{ width: "100%" }}
                placeholder={t("form.fields.quotaFiles.placeholder")}
              />
            </Form.Item>
          </Col>
        </Row>

        <Form.Item
          name="bandwidthKbps"
          label={t("form.fields.bandwidth.label")}
          tooltip={t("form.fields.bandwidth.tooltip")}
        >
          <InputNumber
            min={0}
            style={{ width: "100%" }}
            placeholder={t("form.fields.bandwidth.placeholder")}
            suffix={t("units.kbps")}
          />
        </Form.Item>

        <Form.Item
          name="requirePasswordChange"
          label={t("form.fields.requirePasswordChange.label")}
          valuePropName="checked"
        >
          <Switch
            checkedChildren={t("actions.yes", { ns: "common" })}
            unCheckedChildren={t("actions.no", { ns: "common" })}
          />
        </Form.Item>

        {canInitDirs && (
          <Form.Item
            name="initializeDirs"
            valuePropName="checked"
            label={
              <Trans
                t={t}
                i18nKey="form.fields.initializeDirs.label"
                values={{ dirs: initDirs?.join(" ") ?? "" }}
                components={{ code: <Text code /> }}
              />
            }
            tooltip={t("form.fields.initializeDirs.tooltip", {
              dirs: initDirs?.join(", ") ?? "",
            })}
          >
            <Switch
              checkedChildren={t("actions.yes", { ns: "common" })}
              unCheckedChildren={t("actions.no", { ns: "common" })}
            />
          </Form.Item>
        )}

        {isEdit && (
          <Alert
            type="info"
            showIcon
            title={t("form.editPasswordInfo")}
          />
        )}
      </Form>
    </Modal>
  );
}
