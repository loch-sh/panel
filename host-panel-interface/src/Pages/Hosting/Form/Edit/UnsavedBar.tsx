import { ExclamationCircleOutlined } from "@ant-design/icons";
import { Affix, Button, Flex, Typography, theme } from "antd";
import { ReactNode } from "react";
import { useTranslation } from "react-i18next";

interface Props {
  onSave: () => void;
  saveLabel: string;
  saving: boolean;
  saveDisabled: boolean;
  /** e.g. the "Discard changes" button */
  extraActions?: ReactNode;
}

/** Bar stuck to the bottom of the screen while there are unsaved changes. */
export default function UnsavedBar({
  onSave,
  saveLabel,
  saving,
  saveDisabled,
  extraActions,
}: Readonly<Props>) {
  const { t } = useTranslation("hosting");
  const { token } = theme.useToken();
  return (
    <Affix offsetBottom={0}>
      <Flex
        role="region"
        aria-label={t("form.layout.unsaved")}
        justify="space-between"
        align="center"
        gap="small"
        wrap
        style={{
          marginTop: token.margin,
          padding: `${token.paddingSM}px ${token.padding}px`,
          background: token.colorBgElevated,
          border: `1px solid ${token.colorBorderSecondary}`,
          borderRadius: token.borderRadiusLG,
          boxShadow: token.boxShadowSecondary,
        }}
      >
        <Typography.Text>
          <ExclamationCircleOutlined style={{ color: token.colorWarning }} />{" "}
          {t("form.layout.unsaved")}
        </Typography.Text>
        <Flex gap="small">
          {extraActions}
          <Button
            type="primary"
            loading={saving}
            disabled={saveDisabled}
            onClick={onSave}
          >
            {saveLabel}
          </Button>
        </Flex>
      </Flex>
    </Affix>
  );
}
