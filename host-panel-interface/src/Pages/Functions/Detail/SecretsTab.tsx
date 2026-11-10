import { DeleteOutlined, EditOutlined, PlusOutlined } from "@ant-design/icons";
import {
  Alert,
  Button,
  Card,
  Flex,
  Form,
  Input,
  Modal,
  Space,
  Table,
  Typography,
} from "antd";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { toast } from "react-toastify";
import {
  useFunctionSecretDelete,
  useFunctionSecretSet,
} from "../../../Services/Functions/Hooks/useFunctionMutations";
import {
  ENV_NAME_PATTERN,
  envValueProblem,
  FunctionDetailModel,
  isReservedEnvName,
  MAX_ENV_ENTRIES,
} from "../../../Services/Functions/Models/functions.model";
import { getApiErrorMessage } from "../../../Utils/apiErrors";

const { Text } = Typography;

interface Editing {
  /** replacing an existing secret (its name is fixed) */
  replace: boolean;
  name: string;
}

/**
 * Secrets of a function: names only. A value is typed once, sent, and
 * cleared from the page; it can be replaced or removed, never read.
 */
export default function SecretsTab({
  fn,
}: Readonly<{ fn: FunctionDetailModel }>) {
  const { t } = useTranslation("functions");
  const {
    mutateAsync: setSecret,
    isPending: saving,
    reset: resetSecret,
  } = useFunctionSecretSet();
  const { mutateAsync: removeSecret, isPending: removing } =
    useFunctionSecretDelete();
  const [editing, setEditing] = useState<Editing | null>(null);
  const [name, setName] = useState("");
  const [value, setValue] = useState("");
  const [touched, setTouched] = useState(false);
  const secrets = fn.secrets ?? [];
  const used = secrets.length + (fn.env ?? []).length;

  const nameError = editing?.replace
    ? undefined
    : !name
      ? t("env.nameRequired")
      : !ENV_NAME_PATTERN.test(name)
        ? t("env.nameInvalid")
        : isReservedEnvName(name)
          ? t("env.nameReserved")
          : secrets.includes(name) ||
              (fn.env ?? []).some((e) => e.name === name)
            ? t("env.nameDuplicate")
            : undefined;
  const valueProblem = envValueProblem(value);
  const valueError = valueProblem ? t(`env.value.${valueProblem}`) : undefined;

  const open = (e: Editing) => {
    setEditing(e);
    setName(e.name);
    setValue("");
    setTouched(false);
  };
  const close = () => {
    setEditing(null);
    setName("");
    // The value never stays in the page (nor in the mutation's variables).
    setValue("");
    resetSecret();
  };
  const save = async () => {
    setTouched(true);
    if (!editing || nameError || valueError) return;
    try {
      await setSecret({ name: fn.name, key: name, value });
      toast.success(t("secrets.saved", { name }));
      close();
    } catch (err) {
      toast.error(getApiErrorMessage(err));
    }
  };
  const askRemove = (key: string) =>
    Modal.confirm({
      title: t("secrets.removeConfirm.title", { name: key }),
      content: t("secrets.removeConfirm.content"),
      okText: t("secrets.remove"),
      okButtonProps: { danger: true },
      cancelText: t("actions.cancel", { ns: "common" }),
      onOk: async () => {
        try {
          await removeSecret({ name: fn.name, key });
          toast.success(t("secrets.removed", { name: key }));
        } catch (err) {
          toast.error(getApiErrorMessage(err));
        }
      },
    });

  return (
    <Card
      title={t("secrets.title")}
      extra={
        <Button
          icon={<PlusOutlined />}
          disabled={used >= MAX_ENV_ENTRIES}
          onClick={() => open({ replace: false, name: "" })}
        >
          {t("secrets.add")}
        </Button>
      }
    >
      <Flex vertical gap="middle">
        <Alert type="info" showIcon title={t("secrets.intro")} />
        <Table<{ name: string }>
          dataSource={secrets.map((s) => ({ name: s }))}
          rowKey="name"
          pagination={false}
          locale={{ emptyText: t("secrets.empty") }}
          columns={[
            {
              title: t("secrets.columns.name"),
              dataIndex: "name",
              render: (n: string) => <Text code>{n}</Text>,
            },
            {
              title: t("secrets.columns.value"),
              key: "value",
              render: () => <Text type="secondary">{t("secrets.hidden")}</Text>,
            },
            {
              title: t("secrets.columns.actions"),
              key: "actions",
              render: (_: unknown, r) => (
                <Space>
                  <Button
                    size="small"
                    icon={<EditOutlined />}
                    aria-label={t("secrets.replaceOf", { name: r.name })}
                    disabled={removing}
                    onClick={() => open({ replace: true, name: r.name })}
                  >
                    {t("secrets.replace")}
                  </Button>
                  <Button
                    size="small"
                    danger
                    icon={<DeleteOutlined />}
                    aria-label={t("secrets.removeOf", { name: r.name })}
                    disabled={removing}
                    onClick={() => askRemove(r.name)}
                  >
                    {t("secrets.remove")}
                  </Button>
                </Space>
              ),
            },
          ]}
        />
      </Flex>
      {/* Unmounted once closed: the typed value leaves the DOM at once
          (a closing antd modal keeps its last content while it animates). */}
      {editing && (
        <Modal
          open
          title={
            editing?.replace
              ? t("secrets.replaceTitle", { name: editing.name })
              : t("secrets.addTitle")
          }
          okText={t("actions.save", { ns: "common" })}
          cancelText={t("actions.cancel", { ns: "common" })}
          confirmLoading={saving}
          onOk={() => void save()}
          onCancel={close}
          destroyOnHidden
        >
          <Flex vertical gap="middle">
            <Alert type="warning" showIcon title={t("env.secret.warning")} />
            <Form layout="vertical" onFinish={() => void save()}>
              {!editing?.replace && (
                <Form.Item
                  label={t("secrets.nameLabel")}
                  htmlFor="fn-secret-name"
                  validateStatus={touched && nameError ? "error" : undefined}
                  help={touched ? nameError : undefined}
                >
                  <Input
                    id="fn-secret-name"
                    value={name}
                    onChange={(e) => setName(e.target.value.trim())}
                    autoComplete="off"
                    spellCheck={false}
                    style={{ fontFamily: "monospace" }}
                  />
                </Form.Item>
              )}
              <Form.Item
                label={t("secrets.valueLabel")}
                htmlFor="fn-secret-value"
                validateStatus={valueError ? "error" : undefined}
                help={valueError}
                style={{ marginBottom: 0 }}
              >
                <Input.Password
                  id="fn-secret-value"
                  value={value}
                  onChange={(e) => setValue(e.target.value)}
                  autoComplete="new-password"
                  visibilityToggle={false}
                />
              </Form.Item>
            </Form>
            {fn.state === "running" && (
              <Text type="secondary">{t("secrets.restart")}</Text>
            )}
          </Flex>
        </Modal>
      )}
    </Card>
  );
}
