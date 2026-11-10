import { CheckCircleFilled } from "@ant-design/icons";
import { Card, Flex, Tag, Typography, theme } from "antd";
import { KeyboardEvent } from "react";
import { useTranslation } from "react-i18next";
import {
  HostingTemplate,
  templateHasPhp,
} from "../../../Services/Hosting/Models/hosting.model";
import { familyOf } from "./formPaths";
import { templateIcon } from "../templateMeta";

interface Props {
  template: HostingTemplate;
  selected: boolean;
  disabled: boolean;
  onSelect: () => void;
}

/** One selectable template: icon, name, description and badges. */
export default function TemplateCard({
  template,
  selected,
  disabled,
  onSelect,
}: Readonly<Props>) {
  const { t } = useTranslation("hosting");
  const { token } = theme.useToken();

  const onKeyDown = (e: KeyboardEvent) => {
    if (e.key === "Enter" || e.key === " ") {
      e.preventDefault();
      onSelect();
    }
  };

  return (
    <Card
      hoverable={!disabled}
      role="radio"
      aria-checked={selected}
      aria-disabled={disabled}
      tabIndex={disabled ? -1 : 0}
      onClick={onSelect}
      onKeyDown={onKeyDown}
      style={{
        height: "100%",
        cursor: disabled ? "not-allowed" : "pointer",
        opacity: disabled ? 0.5 : 1,
        borderColor: selected ? token.colorPrimary : undefined,
        background: selected ? token.colorPrimaryBg : undefined,
        boxShadow: selected ? `0 0 0 1px ${token.colorPrimary}` : undefined,
      }}
    >
      <Flex vertical gap={token.marginSM}>
        <Flex justify="space-between" align="flex-start">
          <span
            aria-hidden
            style={{
              fontSize: token.fontSizeHeading2,
              lineHeight: 1,
              color: selected ? token.colorPrimary : token.colorTextSecondary,
            }}
          >
            {templateIcon(template)}
          </span>
          {selected && (
            <CheckCircleFilled
              style={{ color: token.colorPrimary, fontSize: token.fontSizeLG }}
            />
          )}
        </Flex>
        <Typography.Text strong style={{ fontSize: token.fontSizeLG }}>
          {t(`templates.${template}.label`)}
        </Typography.Text>
        <Typography.Text
          type="secondary"
          style={{ fontSize: token.fontSizeSM }}
        >
          {t(`templates.${template}.description`)}
        </Typography.Text>
        <Flex gap={4} wrap>
          {templateHasPhp(template) ? (
            <Tag color="purple">PHP</Tag>
          ) : (
            <Tag>{t("templates.badges.static")}</Tag>
          )}
          <Tag>{familyOf(template)}</Tag>
        </Flex>
      </Flex>
    </Card>
  );
}
