import { Form, Select } from "antd";
import type { TFunction } from "i18next";
import { useTranslation } from "react-i18next";
import { normalizeAssetDirs } from "../formPaths";
import { isValidAssetDir } from "../validators";

const MAX_ASSET_DIRS = 20;

/** Error message for a list of asset directories, or null when valid. */
function assetDirsError(t: TFunction<"hosting">, value?: string[]) {
  const dirs = normalizeAssetDirs(value);
  if (dirs.length > MAX_ASSET_DIRS) {
    return t("form.fields.routing.assetDirs.tooMany", { max: MAX_ASSET_DIRS });
  }
  const invalid = dirs.find((d) => !isValidAssetDir(d));
  if (invalid) {
    return t("form.fields.routing.assetDirs.invalid", { dir: invalid });
  }
  const duplicate = dirs.find((d, i) => dirs.indexOf(d) !== i);
  if (duplicate) {
    return t("form.fields.routing.assetDirs.duplicate", { dir: duplicate });
  }
  return null;
}

/** Symfony asset directories (served as static files only). */
export default function AssetDirsField({
  disabled,
}: Readonly<{ disabled: boolean }>) {
  const { t } = useTranslation("hosting");
  return (
    <Form.Item
      name="assetDirs"
      label={t("form.fields.routing.assetDirs.label")}
      extra={t("form.fields.routing.assetDirs.help")}
      // Same normalization as the payload: "bundles/" is "bundles".
      normalize={(value?: string[]) => normalizeAssetDirs(value)}
      rules={[
        {
          validator: async (_, value?: string[]) => {
            const error = assetDirsError(t, value);
            if (error) throw new Error(error);
          },
        },
      ]}
    >
      <Select
        mode="tags"
        open={false}
        suffixIcon={null}
        disabled={disabled}
        tokenSeparators={[",", " "]}
        placeholder={t("form.fields.routing.assetDirs.placeholder")}
        aria-label={t("form.fields.routing.assetDirs.label")}
      />
    </Form.Item>
  );
}
