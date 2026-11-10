import { Alert, Button, Flex, Form, Spin, Typography } from "antd";
import { useId } from "react";
import { useTranslation } from "react-i18next";
import useHostingPhpModules from "../../../Services/Hosting/Hooks/useHostingPhpModules";
import { getApiErrorMessage } from "../../../Utils/apiErrors";
import { HostingFormValues } from "./formValues";
import PhpModulesPicker from "./PhpModulesPicker";

/** PHP modules of the selected image, with their enabled / disabled state. */
export default function PhpModulesField() {
  const { t } = useTranslation("hosting");
  const labelId = useId();
  const form = Form.useFormInstance<HostingFormValues>();
  const template = Form.useWatch("template", form);
  const version = Form.useWatch("phpVersion", form);
  const { data, error, isFetching, refetch } = useHostingPhpModules(
    template,
    version,
  );

  let content;
  if (!version) {
    content = (
      <Typography.Text type="secondary">
        {t("form.fields.phpModules.selectVersion")}
      </Typography.Text>
    );
  } else if (isFetching && !data) {
    content = (
      <Flex gap="middle" align="center">
        <Spin />
        <Flex vertical>
          <Typography.Text>
            {t("form.fields.phpModules.loading")}
          </Typography.Text>
          <Typography.Text type="secondary">
            {t("form.fields.phpModules.loadingHint")}
          </Typography.Text>
        </Flex>
      </Flex>
    );
  } else if (error) {
    content = (
      <Alert
        type="error"
        showIcon
        title={t("form.fields.phpModules.error")}
        description={getApiErrorMessage(error)}
        action={
          <Button size="small" onClick={() => void refetch()}>
            {t("form.fields.phpModules.retry")}
          </Button>
        }
      />
    );
  }

  return (
    <Form.Item
      label={<span id={labelId}>{t("form.fields.phpModules.label")}</span>}
      extra={t("form.fields.phpModules.help")}
    >
      {content}
      {/* Always mounted, so the list is submitted while modules load. */}
      <Form.Item name={["php", "disabledModules"]} noStyle hidden={!!content}>
        <PhpModulesPicker modules={data} labelId={labelId} />
      </Form.Item>
    </Form.Item>
  );
}
