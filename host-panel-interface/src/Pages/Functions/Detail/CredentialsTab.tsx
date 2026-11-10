import { SyncOutlined } from "@ant-design/icons";
import {
  Alert,
  Button,
  Card,
  Descriptions,
  Flex,
  Modal,
  Switch,
  Typography,
} from "antd";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { toast } from "react-toastify";
import { useFunctionRotateCredentials } from "../../../Services/Functions/Hooks/useFunctionMutations";
import {
  FUNCTION_REGION,
  FunctionCredentialsModel,
  FunctionDetailModel,
} from "../../../Services/Functions/Models/functions.model";
import { getApiErrorMessage } from "../../../Utils/apiErrors";
import FunctionCredentialsModal from "../FunctionCredentialsModal";

const { Text } = Typography;

/**
 * Invoke credentials: never readable again. A rotation (optionally turning
 * SigV4 on or off) returns new ones, shown once.
 */
export default function CredentialsTab({
  fn,
}: Readonly<{ fn: FunctionDetailModel }>) {
  const { t } = useTranslation("functions");
  const {
    mutateAsync: rotate,
    isPending,
    reset: resetRotate,
  } = useFunctionRotateCredentials();
  const [credentials, setCredentials] =
    useState<FunctionCredentialsModel | null>(null);

  const ask = (sigv4?: boolean) => {
    const mode =
      sigv4 === undefined ? "rotate" : sigv4 ? "enableSigv4" : "disableSigv4";
    Modal.confirm({
      title: t(`credentialsTab.${mode}.title`, { name: fn.name }),
      content: (
        <Flex vertical gap="small">
          <Text>{t(`credentialsTab.${mode}.content`)}</Text>
          {sigv4 && (
            <Alert type="warning" showIcon title={t("sigv4.warning")} />
          )}
        </Flex>
      ),
      okText: t(`credentialsTab.${mode}.submit`),
      okButtonProps: { danger: sigv4 === undefined },
      cancelText: t("actions.cancel", { ns: "common" }),
      onOk: async () => {
        try {
          const c = await rotate({ name: fn.name, sigv4 });
          setCredentials(c);
          toast.success(t("toasts.rotated", { name: fn.name }));
        } catch (err) {
          toast.error(getApiErrorMessage(err));
        }
      },
    });
  };

  return (
    <Flex vertical gap="middle">
      <Card title={t("credentialsTab.title")}>
        <Flex vertical gap="middle">
          <Text type="secondary">{t("credentialsTab.intro")}</Text>
          <Descriptions
            column={1}
            size="small"
            items={[
              {
                key: "endpoint",
                label: t("credentials.fields.endpoint"),
                children: (
                  <Text code copyable style={{ wordBreak: "break-all" }}>
                    {fn.endpoint}
                  </Text>
                ),
              },
              {
                key: "region",
                label: t("credentials.fields.region"),
                children: <Text code>{FUNCTION_REGION}</Text>,
              },
              {
                key: "bearer",
                label: t("credentials.fields.token"),
                children: t("credentialsTab.bearerStored"),
              },
            ]}
          />
          <Button
            icon={<SyncOutlined />}
            loading={isPending}
            onClick={() => ask()}
            style={{ alignSelf: "flex-start" }}
          >
            {t("credentialsTab.rotate.submit")}
          </Button>
        </Flex>
      </Card>
      <Card title={t("sigv4.label")}>
        <Flex vertical gap="small">
          <Flex gap="small" align="center">
            <Switch
              checked={fn.sigv4}
              loading={isPending}
              onChange={(on) => ask(on)}
              aria-label={t("sigv4.label")}
            />
            <Text>{fn.sigv4 ? t("sigv4.enabled") : t("sigv4.disabled")}</Text>
          </Flex>
          <Text type="secondary">{t("sigv4.help")}</Text>
          {fn.sigv4 && (
            <Alert type="warning" showIcon title={t("sigv4.warning")} />
          )}
        </Flex>
      </Card>
      <FunctionCredentialsModal
        credentials={credentials}
        onClose={() => {
          setCredentials(null);
          resetRotate();
        }}
      />
    </Flex>
  );
}
