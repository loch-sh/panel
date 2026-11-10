import { Typography } from "antd";
import { useTranslation } from "react-i18next";

/** Discreet trademark and non-affiliation notice of the functions pages. */
export default function TrademarkNotice() {
  const { t } = useTranslation("functions");
  return (
    <Typography.Text type="secondary" style={{ fontSize: 12 }}>
      {t("trademarkNotice")}
    </Typography.Text>
  );
}
