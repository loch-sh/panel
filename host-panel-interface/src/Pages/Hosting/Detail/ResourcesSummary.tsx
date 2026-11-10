import { useTranslation } from "react-i18next";
import { HostingResources } from "../../../Services/Hosting/Models/hosting.model";

/** Memory and CPU limits of a stack ("Unlimited" when 0). */
export default function ResourcesSummary({
  resources,
}: Readonly<{ resources: HostingResources }>) {
  const { t } = useTranslation("hosting");
  const unlimited = t("detail.overview.unlimited");
  return (
    <>
      {t("detail.overview.resourcesValue", {
        memory:
          resources.memoryMB > 0
            ? t("detail.overview.memoryValue", { value: resources.memoryMB })
            : unlimited,
        cpus:
          resources.cpus > 0
            ? t("detail.overview.cpusValue", { value: resources.cpus })
            : unlimited,
      })}
    </>
  );
}
