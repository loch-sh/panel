import {
  Alert,
  AutoComplete,
  Button,
  Descriptions,
  Flex,
  Form,
  InputNumber,
  Modal,
  Switch,
  Typography,
} from "antd";
import { isAxiosError } from "axios";
import { useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { toast } from "react-toastify";
import { useInstanceExposure } from "../../Services/Databases/Hooks/useDatabaseMutations";
import {
  ExposureLimitsModel,
  ServiceEngineModel,
  ServiceInstanceModel,
  findVersion,
} from "../../Services/Databases/Models/databases.model";
import {
  addressAllowed,
  isLoopback,
  parseIPv4,
  singleAddresses,
} from "../../Services/Networks/subnet";
import { getApiErrorMessage } from "../../Utils/apiErrors";

const { Text } = Typography;

interface Props {
  /** null: closed */
  instance: ServiceInstanceModel | null;
  engine: ServiceEngineModel | undefined;
  limits: ExposureLimitsModel | undefined;
  onClose: () => void;
}

interface FormValues {
  address: string;
  port: number | null;
  tlsOptional: boolean;
}

/** "[::1]:5432" or "127.0.0.1:5432". */
export function formatHostPort(address: string, port: number): string {
  return `${address.includes(":") ? `[${address}]` : address}:${port}`;
}

/** Why an instance may only be published on a loopback address, if so. */
export function loopbackOnlyReason(
  instance: ServiceInstanceModel,
  engine: ServiceEngineModel | undefined,
  limits: ExposureLimitsModel,
): "deprecated" | "unauthenticated" | "noTls" | null {
  if (instance.deprecated) return "deprecated";
  if (instance.unauthenticated || engine?.unauthenticated) {
    return "unauthenticated";
  }
  if (
    limits.tlsMode === "auto" &&
    !findVersion(engine, instance.version)?.tls
  ) {
    return "noTls";
  }
  return null;
}

const looksLikeAddress = (v: string) =>
  parseIPv4(v) !== null || /^[0-9a-f:]+$/i.test(v);

function ExposureForm({
  instance,
  engine,
  limits,
  onClose,
}: Readonly<{
  instance: ServiceInstanceModel;
  engine: ServiceEngineModel | undefined;
  limits: ExposureLimitsModel;
  onClose: () => void;
}>) {
  const { t } = useTranslation("databases");
  const [form] = Form.useForm<FormValues>();
  const { set, close } = useInstanceExposure();
  const busy = set.isPending || close.isPending;
  const [serverError, setServerError] = useState<string>();
  const current = instance.exposure;

  const suggestions = useMemo(() => {
    const list = singleAddresses(limits.allowedCidrs);
    if (
      !list.includes("127.0.0.1") &&
      addressAllowed("127.0.0.1", limits.allowedCidrs)
    ) {
      list.push("127.0.0.1");
    }
    return list;
  }, [limits.allowedCidrs]);

  const defaultPort =
    engine &&
    engine.defaultPort >= limits.minPort &&
    engine.defaultPort <= limits.maxPort
      ? engine.defaultPort
      : limits.minPort;
  const initialValues: FormValues = {
    address: current?.address ?? suggestions[0] ?? "",
    port: current?.port ?? defaultPort,
    tlsOptional: current?.tlsOptional ?? false,
  };

  const address = (
    Form.useWatch("address", form) ?? initialValues.address
  ).trim();
  const loopback = isLoopback(address);
  const restricted = loopbackOnlyReason(instance, engine, limits);
  const validAddress = address !== "" && looksLikeAddress(address);
  const publicAddress = validAddress && !loopback;
  const tlsRequired = publicAddress && limits.tlsMode === "auto";
  const tlsOptionalAvailable =
    tlsRequired &&
    limits.allowTlsOptional &&
    !!findVersion(engine, instance.version)?.tlsOptional;

  const applyError = (err: unknown) => {
    const field =
      isAxiosError(err) &&
      (err.response?.data as { field?: unknown } | undefined)?.field;
    const message = getApiErrorMessage(err);
    if (field === "address" || field === "port" || field === "tlsOptional") {
      form.setFields([{ name: field, errors: [message] }]);
      setServerError(undefined);
    } else {
      setServerError(message);
    }
  };

  const submit = async () => {
    let values: FormValues;
    try {
      values = await form.validateFields();
    } catch {
      return;
    }
    setServerError(undefined);
    const addr = values.address.trim();
    try {
      await set.mutateAsync({
        instance: instance.key,
        exposure: {
          address: addr,
          port: values.port ?? 0,
          tlsOptional: tlsOptionalAvailable && values.tlsOptional,
        },
      });
      toast.success(
        t("exposure.toasts.published", {
          address: formatHostPort(addr, values.port ?? 0),
        }),
      );
      onClose();
    } catch (err) {
      applyError(err);
    }
  };

  const closePort = () => {
    Modal.confirm({
      title: t("exposure.closeConfirm.title", { name: instance.project }),
      content: t("exposure.closeConfirm.content"),
      okText: t("exposure.closeConfirm.submit"),
      okButtonProps: { danger: true },
      cancelText: t("actions.cancel", { ns: "common" }),
      onOk: async () => {
        try {
          await close.mutateAsync(instance.key);
          toast.success(t("exposure.toasts.closed"));
          onClose();
        } catch (err) {
          setServerError(getApiErrorMessage(err));
        }
      },
    });
  };

  const noAddress = limits.allowedCidrs.length === 0;

  return (
    <Modal
      open
      title={t("exposure.title", { name: instance.project })}
      onCancel={onClose}
      destroyOnHidden
      footer={
        <Flex justify="space-between" gap="small" wrap>
          <div>
            {current && (
              <Button danger disabled={busy} onClick={closePort}>
                {t("exposure.close")}
              </Button>
            )}
          </div>
          <Flex gap="small">
            <Button onClick={onClose} disabled={busy}>
              {t("actions.cancel", { ns: "common" })}
            </Button>
            <Button
              type="primary"
              loading={set.isPending}
              disabled={busy || noAddress}
              onClick={() => void submit()}
            >
              {t("exposure.submit")}
            </Button>
          </Flex>
        </Flex>
      }
    >
      <Flex vertical gap="middle">
        <Text type="secondary">{t("exposure.intro")}</Text>
        {current ? (
          <Descriptions
            size="small"
            column={1}
            bordered
            items={[
              {
                key: "current",
                label: t("exposure.current"),
                children: (
                  <Text code>
                    {formatHostPort(current.address, current.port)}
                  </Text>
                ),
              },
              {
                key: "tls",
                label: t("exposure.tls"),
                children: current.tls
                  ? current.tlsOptional
                    ? t("instances.tlsOptional")
                    : t("instances.tls")
                  : t("instances.noTls"),
              },
              ...(current.tls && current.fingerprint
                ? [
                    {
                      key: "fingerprint",
                      label: t("exposure.fingerprint"),
                      children: (
                        <Text code copyable style={{ wordBreak: "break-all" }}>
                          {current.fingerprint}
                        </Text>
                      ),
                    },
                  ]
                : []),
            ]}
          />
        ) : (
          <Alert type="info" showIcon message={t("exposure.notPublished")} />
        )}
        {instance.exposureError && (
          <Alert
            type="error"
            showIcon
            message={t("instances.exposureErrorHint")}
          />
        )}
        {noAddress ? (
          <Alert type="warning" showIcon message={t("exposure.addressNone")} />
        ) : (
          <Form
            form={form}
            layout="vertical"
            requiredMark={false}
            initialValues={initialValues}
            disabled={busy}
          >
            {restricted && (
              <Alert
                type="info"
                showIcon
                style={{ marginBottom: 16 }}
                message={t(`exposure.loopbackOnly.${restricted}`)}
              />
            )}
            <Flex gap="middle" wrap>
              <Form.Item
                name="address"
                label={t("exposure.address")}
                extra={t("exposure.addressHelp", {
                  cidrs: limits.allowedCidrs.join(", "),
                })}
                style={{ flex: "1 1 240px" }}
                validateTrigger="onChange"
                rules={[
                  {
                    validator: (_, raw?: string) => {
                      const v = (raw ?? "").trim();
                      if (!v) {
                        return Promise.reject(
                          new Error(t("exposure.addressRequired")),
                        );
                      }
                      if (!looksLikeAddress(v)) {
                        return Promise.reject(
                          new Error(t("exposure.addressInvalid")),
                        );
                      }
                      if (addressAllowed(v, limits.allowedCidrs) === false) {
                        return Promise.reject(
                          new Error(t("exposure.addressNotAllowed")),
                        );
                      }
                      if (restricted && !isLoopback(v)) {
                        return Promise.reject(
                          new Error(t(`exposure.loopbackOnly.${restricted}`)),
                        );
                      }
                      return Promise.resolve();
                    },
                  },
                ]}
              >
                <AutoComplete
                  options={suggestions.map((s) => ({ value: s }))}
                  placeholder="127.0.0.1"
                  aria-label={t("exposure.address")}
                />
              </Form.Item>
              <Form.Item
                name="port"
                label={t("exposure.port")}
                extra={t("exposure.portHelp", {
                  min: limits.minPort,
                  max: limits.maxPort,
                })}
                style={{ flex: "0 1 160px" }}
                validateTrigger="onChange"
                rules={[
                  {
                    validator: (_, port?: number | null) => {
                      if (port === null || port === undefined) {
                        return Promise.reject(
                          new Error(t("exposure.portRequired")),
                        );
                      }
                      if (
                        !Number.isInteger(port) ||
                        port < limits.minPort ||
                        port > limits.maxPort
                      ) {
                        return Promise.reject(
                          new Error(
                            t("exposure.portRange", {
                              min: limits.minPort,
                              max: limits.maxPort,
                            }),
                          ),
                        );
                      }
                      if (limits.reservedPorts.includes(port)) {
                        return Promise.reject(
                          new Error(t("exposure.portReserved")),
                        );
                      }
                      return Promise.resolve();
                    },
                  },
                ]}
              >
                <InputNumber
                  aria-label={t("exposure.port")}
                  precision={0}
                  controls={false}
                  style={{ width: "100%" }}
                />
              </Form.Item>
            </Flex>
            {tlsOptionalAvailable && (
              <Form.Item
                name="tlsOptional"
                label={t("exposure.tlsOptional")}
                tooltip={t("exposure.tlsOptionalHelp")}
                valuePropName="checked"
              >
                <Switch aria-label={t("exposure.tlsOptional")} />
              </Form.Item>
            )}
            {validAddress && loopback && (
              <Alert
                type="info"
                showIcon
                message={t("exposure.loopbackNote")}
              />
            )}
            {tlsRequired && (
              <Alert
                type="warning"
                showIcon
                message={t("exposure.tlsRequired")}
              />
            )}
            {publicAddress && limits.tlsMode === "off" && (
              <Alert
                type="warning"
                showIcon
                message={t("exposure.noEncryption")}
              />
            )}
          </Form>
        )}
        {serverError && <Alert type="error" showIcon message={serverError} />}
      </Flex>
    </Modal>
  );
}

/**
 * Publishes the port of an instance on an address of the server (or closes
 * it), within the exposure policy of the API configuration. The rules are
 * mirrored as hints: the server stays the source of truth.
 */
export default function ExposureModal({
  instance,
  engine,
  limits,
  onClose,
}: Readonly<Props>) {
  if (!instance || !limits) return null;
  return (
    <ExposureForm
      key={instance.key}
      instance={instance}
      engine={engine}
      limits={limits}
      onClose={onClose}
    />
  );
}
