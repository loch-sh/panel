import { TranslationOutlined } from "@ant-design/icons";
import { Flex, Select, theme } from "antd";
import { useTranslation } from "react-i18next";
import { Language, SUPPORTED_LANGUAGES } from "../../i18n";
import { useLanguage } from "../../i18n/useLocales";

interface Props {
  collapsed: boolean;
}

export default function LanguageSwitcher({ collapsed }: Readonly<Props>) {
  const { t, i18n } = useTranslation();
  const { token } = theme.useToken();
  const language = useLanguage();

  // Language names are written in their own language (see common.language.*)
  const options = SUPPORTED_LANGUAGES.map((lng) => ({
    value: lng,
    label: collapsed ? lng.toUpperCase() : t(`language.${lng}`),
  }));

  return (
    <Flex
      align="center"
      justify="center"
      gap="small"
      style={{
        padding: "12px 16px",
        borderTop: `1px solid ${token.colorBorderSecondary}`,
      }}
    >
      {!collapsed && (
        <TranslationOutlined
          aria-hidden
          style={{ color: token.colorTextSecondary }}
        />
      )}
      <Select<Language>
        aria-label={t("language.label")}
        value={language}
        options={options}
        onChange={(lng) => void i18n.changeLanguage(lng)}
        variant="borderless"
        size="small"
        popupMatchSelectWidth={false}
        style={{ flex: collapsed ? undefined : 1 }}
      />
    </Flex>
  );
}
