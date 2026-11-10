import { Form } from "antd";
import { useTranslation } from "react-i18next";
import useNetworks from "../../../Services/Networks/Hooks/useNetworks";
import NetworkSelect from "../../Networks/NetworkSelect";

/**
 * Private networks the site joins (the shared "services" network gives access
 * to the services on it). Preselected at creation from the server defaults.
 */
export default function NetworksField() {
  const { t } = useTranslation("hosting");
  const { data: networks, isLoading } = useNetworks();
  return (
    <Form.Item
      name="networks"
      label={t("form.fields.networks.label")}
      tooltip={t("form.fields.networks.help")}
      extra={t("form.fields.networks.extra")}
    >
      <NetworkSelect
        aria-label={t("form.fields.networks.label")}
        networks={networks}
        loading={isLoading}
        showCreateLink
      />
    </Form.Item>
  );
}
