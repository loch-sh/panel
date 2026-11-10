import type { FormInstance } from "antd";
import { isDev } from "../../../../config";
import { HostingFormValues } from "../formValues";

/** Sections of the stack form (wizard steps in creation, cards in edition). */
export const SECTION_KEYS = ["type", "site", "runtime", "advanced"] as const;
export type SectionKey = (typeof SECTION_KEYS)[number];

/** Collapsible panels holding fields (opened when one of them has an error). */
export type PanelKey = "phpModules" | "routing" | "env" | "phpIni" | "vhost";

type FieldName = (string | number)[];
/** A list of names accepted by the form methods (validateFields...). */
type FormNames = NonNullable<
  Parameters<FormInstance<HostingFormValues>["getFieldsError"]>[0]
>;

const SECTION_OF_ROOT: Record<string, SectionKey> = {
  template: "type",
  name: "site",
  siteMode: "site",
  site: "site",
  documentRoot: "site",
  domains: "site",
  https: "site",
  redirectHttps: "site",
  phpVersion: "runtime",
  php: "runtime",
  resources: "runtime",
  framework: "advanced",
  assetDirs: "advanced",
  customRouting: "advanced",
  routingRules: "advanced",
  env: "advanced",
  phpIni: "advanced",
  vhost: "advanced",
};

const PANEL_OF_ROOT: Record<string, PanelKey> = {
  framework: "routing",
  assetDirs: "routing",
  customRouting: "routing",
  routingRules: "routing",
  env: "env",
  phpIni: "phpIni",
  vhost: "vhost",
};

/** Unmapped roots already reported (one warning each). */
const warnedRoots = new Set<string>();

/** Section of a field nobody mapped: the last one, so it is never skipped. */
const FALLBACK_SECTION: SectionKey = SECTION_KEYS[SECTION_KEYS.length - 1];

/** Section of a form field, from the first segment of its name. */
export function sectionOfField(name: FieldName): SectionKey {
  const root = String(name[0]);
  const section = SECTION_OF_ROOT[root];
  if (section) return section;
  if (isDev && !warnedRoots.has(root)) {
    warnedRoots.add(root);
    console.warn(`Hosting form: field "${root}" has no section`);
  }
  return FALLBACK_SECTION;
}

/** Collapsible panel holding a form field, if any. */
export function panelOfField(name: FieldName): PanelKey | undefined {
  if (name[0] === "php" && name[1] === "disabledModules") return "phpModules";
  return PANEL_OF_ROOT[String(name[0])];
}

/** Names of the mounted fields of a section (to validate one wizard step). */
export function fieldsOfSection(
  form: FormInstance<HostingFormValues>,
  section: SectionKey,
): FormNames {
  return form
    .getFieldsError()
    .map((f) => f.name)
    .filter((name) => sectionOfField(name) === section) as FormNames;
}

/** Names of the fields currently showing an error. */
export function fieldsWithErrors(
  form: FormInstance<HostingFormValues>,
): FieldName[] {
  return form
    .getFieldsError()
    .filter((f) => f.errors.length > 0)
    .map((f) => f.name);
}
