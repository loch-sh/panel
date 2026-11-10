import { ApartmentOutlined, SettingOutlined } from "@ant-design/icons";
import { Button, Descriptions, Flex, Typography } from "antd";
import { useTranslation } from "react-i18next";
import { ServiceInstanceModel } from "../../Services/Databases/Models/databases.model";
import { NetworkLinks } from "../Networks/NetworkTags";
import { formatHostPort } from "./ExposureModal";

const { Text } = Typography;

interface Props {
  instance: ServiceInstanceModel;
  onNetworks: () => void;
  onExposure: () => void;
}

/** Networks and published port of an instance, each with its change action. */
export default function InstanceAccess({
  instance,
  onNetworks,
  onExposure,
}: Readonly<Props>) {
  const { t } = useTranslation("databases");
  const exposure = instance.exposure;
  return (
    <Descriptions
      column={1}
      size="small"
      items={[
        {
          key: "networks",
          label: t("networks.label"),
          children: (
            <Flex gap="small" align="center" wrap>
              <NetworkLinks networks={instance.networks} />
              <Button
                size="small"
                icon={<ApartmentOutlined />}
                onClick={onNetworks}
              >
                {t("networks.edit")}
              </Button>
            </Flex>
          ),
        },
        {
          key: "exposure",
          label: t("instances.columns.exposure"),
          children: (
            <Flex gap="small" align="center" wrap>
              {exposure ? (
                <Text code>
                  {formatHostPort(exposure.address, exposure.port)}
                </Text>
              ) : (
                <Text type="secondary">{t("instances.notExposed")}</Text>
              )}
              <Button
                size="small"
                icon={<SettingOutlined />}
                onClick={onExposure}
              >
                {t("exposure.edit")}
              </Button>
            </Flex>
          ),
        },
      ]}
    />
  );
}
