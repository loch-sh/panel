import { Typography } from "antd";
import { useTranslation } from "react-i18next";
import { byteLength } from "./validators";

/** UTF-8 size of a snippet against the server limit. */
export default function ByteCounter({
  text,
  max,
}: Readonly<{ text?: string; max: number }>) {
  const { t } = useTranslation("hosting");
  const used = byteLength(text ?? "");
  return (
    <Typography.Text
      type={used > max ? "danger" : "secondary"}
      style={{ display: "block", marginTop: 4, fontSize: 12 }}
    >
      {t("form.fields.bytes", { used, max })}
    </Typography.Text>
  );
}
