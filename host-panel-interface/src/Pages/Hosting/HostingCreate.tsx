import { ArrowLeftOutlined } from "@ant-design/icons";
import { Button, Flex, Form, Skeleton } from "antd";
import { useCallback, useMemo } from "react";
import { useTranslation } from "react-i18next";
import { useNavigate } from "react-router";
import { toast } from "react-toastify";
import { PanelTitle } from "../../Components/Base/PanelTitle";
import useHostingConfig from "../../Services/Hosting/Hooks/useHostingConfig";
import useHostingCreate from "../../Services/Hosting/Hooks/useHostingCreate";
import useHostingSites from "../../Services/Hosting/Hooks/useHostingSites";
import {
  defaultFormValues,
  formValuesToPayload,
  HostingFormValues,
} from "./Form/formValues";
import {
  applyServerFieldError,
  hostingErrorMessage,
} from "./Form/serverErrors";
import HostingStackForm from "./Form/HostingStackForm";
import HostingDisabled from "./HostingDisabled";

export default function HostingCreate() {
  const { t } = useTranslation("hosting");
  const navigate = useNavigate();
  const [form] = Form.useForm<HostingFormValues>();
  const {
    data: config,
    isLoading,
    error: configError,
    refetch: refetchConfig,
  } = useHostingConfig();
  const enabled = config?.enabled ?? false;
  const { data: sites } = useHostingSites(enabled);
  const { mutateAsync: create, isPending } = useHostingCreate();

  const initialValues = useMemo(
    () => (config ? defaultFormValues(config) : undefined),
    [config],
  );

  const handleSubmit = useCallback(
    async (values: HostingFormValues): Promise<boolean> => {
      const payload = formValuesToPayload(values);
      try {
        const res = await create(payload);
        toast.info(t("toasts.createStarted", { name: res.name }));
        navigate(`/hosting/${encodeURIComponent(res.name)}`);
        return true;
      } catch (err) {
        if (!applyServerFieldError(form, err)) {
          toast.error(hostingErrorMessage(err, form.getFieldsValue(true)));
        }
        return false;
      }
    },
    [create, form, navigate, t],
  );

  if (isLoading) return <Skeleton />;
  if (!config || !enabled || !initialValues) {
    return (
      <HostingDisabled
        error={configError}
        onRetry={() => void refetchConfig()}
      />
    );
  }

  return (
    <Flex vertical gap="middle" style={{ maxWidth: 1100 }}>
      <Flex gap="small" align="center">
        <Button
          type="text"
          icon={<ArrowLeftOutlined />}
          aria-label={t("backToList")}
          onClick={() => navigate("/hosting")}
        />
        <PanelTitle>{t("create.title")}</PanelTitle>
      </Flex>

      <HostingStackForm
        form={form}
        mode="create"
        config={config}
        sites={sites}
        initialValues={initialValues}
        submitLabel={t("create.submit")}
        submitting={isPending}
        onSubmit={handleSubmit}
        extraActions={
          <Button onClick={() => navigate("/hosting")}>
            {t("actions.cancel", { ns: "common" })}
          </Button>
        }
      />
    </Flex>
  );
}
