import { Flex, Tag } from "antd";
import { useTranslation } from "react-i18next";
import { HostingSpec } from "../../../Services/Hosting/Models/hosting.model";

/** Routing mode of a stack: default, Symfony (with asset dirs), Laravel or custom. */
export default function RoutingSummary({
  spec,
}: Readonly<{ spec: HostingSpec | null }>) {
  const { t } = useTranslation("hosting");
  switch (spec?.routing) {
    case "symfony":
      return (
        <Flex gap={4} wrap align="center">
          {t("detail.overview.routingSymfony")}
          {(spec.assetDirs ?? []).map((dir) => (
            <Tag key={dir}>{dir}</Tag>
          ))}
        </Flex>
      );
    case "laravel":
      return <>{t("detail.overview.routingLaravel")}</>;
    case "custom":
      return <>{t("detail.overview.routingCustom")}</>;
    default:
      return <>{t("detail.overview.routingDefault")}</>;
  }
}
