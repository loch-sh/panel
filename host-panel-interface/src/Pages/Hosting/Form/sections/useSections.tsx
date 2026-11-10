import type { FormInstance } from "antd";
import type { HostingFormValues } from "../formValues";
import {
  AppstoreOutlined,
  GlobalOutlined,
  SettingOutlined,
  ThunderboltOutlined,
} from "@ant-design/icons";
import { ReactNode } from "react";
import { useTranslation } from "react-i18next";
import AdvancedSection from "./AdvancedSection";
import RuntimeSection from "./RuntimeSection";
import { SectionKey } from "./sectionKeys";
import { SectionProps } from "./sectionProps";
import SiteSection from "./SiteSection";
import TemplateSection from "./TemplateSection";
import usePanelSummaries from "./usePanelSummaries";

export interface SectionDef {
  key: SectionKey;
  icon: ReactNode;
  title: string;
  subtitle: string;
  content: ReactNode;
}

/** The sections of the form, in order, with their header and content. */
export default function useSections(
  formProps: SectionProps,
  form: FormInstance<HostingFormValues>,
): SectionDef[] {
  const { t } = useTranslation("hosting");
  // Computed once for all sections (they watch the form values).
  const summaries = usePanelSummaries(form, formProps.hasPhp);
  const props = { ...formProps, summaries };
  const runtime = props.hasPhp ? "runtime" : "runtimeStatic";
  return [
    {
      key: "type",
      icon: <AppstoreOutlined />,
      title: t("form.layout.type.title"),
      subtitle: t("form.layout.type.subtitle"),
      content: <TemplateSection {...props} />,
    },
    {
      key: "site",
      icon: <GlobalOutlined />,
      title: t("form.layout.site.title"),
      subtitle: t("form.layout.site.subtitle"),
      // Remounted on discard (drops the document root notice).
      content: <SiteSection key={props.resetKey} {...props} />,
    },
    {
      key: "runtime",
      icon: <ThunderboltOutlined />,
      title: t(`form.layout.${runtime}.title`),
      subtitle: t(`form.layout.${runtime}.subtitle`),
      content: <RuntimeSection {...props} />,
    },
    {
      key: "advanced",
      icon: <SettingOutlined />,
      title: t("form.layout.advanced.title"),
      subtitle: t("form.layout.advanced.subtitle"),
      content: <AdvancedSection {...props} />,
    },
  ];
}
