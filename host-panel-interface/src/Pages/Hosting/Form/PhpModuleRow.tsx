import { WarningOutlined } from "@ant-design/icons";
import { Flex, Switch, Tag, Tooltip, Typography } from "antd";
import { useTranslation } from "react-i18next";
import { PhpModuleModel } from "../../../Services/Hosting/Models/hosting.model";

const IONCUBE = "ioncube";

interface Props {
  module: PhpModuleModel;
  /** disabled module taking this one down with it, if any */
  forcedBy?: string;
  /** listed in the disabled modules */
  disabled: boolean;
  onToggle: (enabled: boolean) => void;
}

/** Switch (on = enabled) and name of one PHP module. */
export default function PhpModuleRow({
  module,
  forcedBy,
  disabled,
  onToggle,
}: Readonly<Props>) {
  const { t } = useTranslation("hosting");
  const enabled = !forcedBy && !disabled;
  const control = (
    <Switch
      size="small"
      checked={enabled}
      disabled={!!forcedBy}
      onChange={onToggle}
      aria-label={module.name}
    />
  );

  return (
    <Flex gap={8} align="center">
      {forcedBy ? (
        <Tooltip
          title={t("form.fields.phpModules.disabledWith", { module: forcedBy })}
        >
          <span>{control}</span>
        </Tooltip>
      ) : (
        control
      )}
      <Typography.Text
        code
        type={enabled ? undefined : "secondary"}
        title={module.file}
      >
        {module.name}
      </Typography.Text>
      {module.name === IONCUBE && (
        <Tooltip title={t("form.fields.phpModules.ioncubeWarning")}>
          <Tag color="warning" icon={<WarningOutlined />}>
            {t("form.fields.phpModules.ioncubeTag")}
          </Tag>
        </Tooltip>
      )}
    </Flex>
  );
}
