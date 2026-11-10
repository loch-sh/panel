import { Alert, Flex, Form, Modal } from "antd";
import { isAxiosError } from "axios";
import { memo, useMemo } from "react";
import { useTranslation } from "react-i18next";
import { toast } from "react-toastify";
import useHostingUpdate from "../../../Services/Hosting/Hooks/useHostingUpdate";
import {
  HostingConfigModel,
  HostingStackDetail,
  HostingSpec,
  HOSTING_TEMPLATES,
  templateHasPhp,
} from "../../../Services/Hosting/Models/hosting.model";
import {
  formValuesToPayload,
  HostingFormValues,
  specToFormValues,
} from "../Form/formValues";
import {
  applyServerFieldError,
  hostingErrorMessage,
} from "../Form/serverErrors";
import HostingStackForm from "../Form/HostingStackForm";

interface Props {
  stack: HostingStackDetail & { spec: HostingSpec };
  config: HostingConfigModel;
}

function isManuallyModifiedError(err: unknown): boolean {
  return (
    isAxiosError(err) &&
    (err.response?.data as { code?: string } | undefined)?.code ===
      "HOSTING_MANUALLY_MODIFIED"
  );
}

function SettingsTab({ stack, config }: Readonly<Props>) {
  const { t } = useTranslation("hosting");
  const [form] = Form.useForm<HostingFormValues>();
  const { mutateAsync: update, isPending } = useHostingUpdate();
  const busy = stack.operation?.state === "running";

  const initialValues = useMemo(
    () => specToFormValues(stack.spec, stack.snippets, config),
    [stack.spec, stack.snippets, config],
  );

  // A PHP stack can switch web server, not drop PHP (and vice versa).
  const allowedTemplates = useMemo(() => {
    const hasPhp = templateHasPhp(stack.spec.template);
    return HOSTING_TEMPLATES.filter((tpl) => templateHasPhp(tpl) === hasPhp);
  }, [stack.spec.template]);

  /** Sends the settings; resolves to true once the update is accepted. */
  const send = async (
    values: HostingFormValues,
    force: boolean,
  ): Promise<boolean> => {
    try {
      await update({ payload: formValuesToPayload(values), force });
      toast.info(t("toasts.updateStarted", { name: stack.name }));
      return true;
    } catch (err) {
      if (!force && isManuallyModifiedError(err)) {
        // Edited outside the panel since the page was loaded.
        return confirmForce(values);
      }
      if (!applyServerFieldError(form, err)) {
        toast.error(hostingErrorMessage(err, form.getFieldsValue(true)));
      }
      return false;
    }
  };

  /**
   * Explicit confirmation before overwriting manual compose edits; resolves
   * to the result of the forced update, or false when cancelled.
   */
  const confirmForce = (values: HostingFormValues) =>
    new Promise<boolean>((resolve) => {
      Modal.confirm({
        title: t("settings.forceConfirm.title"),
        content: t("settings.forceConfirm.content", { name: stack.name }),
        okText: t("settings.forceConfirm.ok"),
        okButtonProps: { danger: true },
        cancelText: t("actions.cancel", { ns: "common" }),
        onOk: () => send(values, true).then(resolve),
        onCancel: () => resolve(false),
      });
    });

  const handleSubmit = (values: HostingFormValues) =>
    stack.modified ? confirmForce(values) : send(values, false);

  return (
    <Flex vertical gap="middle">
      {stack.modified && (
        <Alert
          type="warning"
          showIcon
          title={t("settings.modifiedAlert.title")}
          description={t("settings.modifiedAlert.description")}
        />
      )}
      {busy && <Alert type="info" showIcon title={t("actions.busy")} />}
      <HostingStackForm
        form={form}
        mode="edit"
        config={config}
        initialValues={initialValues}
        allowedTemplates={allowedTemplates}
        submitLabel={t("settings.submit")}
        submitting={isPending}
        disabled={busy}
        // Env values and snippets are emptied: saving would wipe them.
        readOnly={!!stack.secretsMasked}
        onSubmit={handleSubmit}
      />
    </Flex>
  );
}

export default memo(SettingsTab);
