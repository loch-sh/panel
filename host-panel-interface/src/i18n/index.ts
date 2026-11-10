import i18n, { type Resource, type ResourceLanguage } from "i18next";
import LanguageDetector from "i18next-browser-languagedetector";
import { initReactI18next } from "react-i18next";

import enApikeys from "./locales/en/apikeys.json";
import enAudit from "./locales/en/audit.json";
import enCommon from "./locales/en/common.json";
import enContainers from "./locales/en/containers.json";
import enCron from "./locales/en/cron.json";
import enDatabases from "./locales/en/databases.json";
import enDomains from "./locales/en/domains.json";
import enErrors from "./locales/en/errors.json";
import enFunctions from "./locales/en/functions.json";
import enHome from "./locales/en/home.json";
import enHosting from "./locales/en/hosting.json";
import enLayout from "./locales/en/layout.json";
import enNetworks from "./locales/en/networks.json";
import enSftp from "./locales/en/sftp.json";
import enUsers from "./locales/en/users.json";

/**
 * English is the source of truth: its shape types every `t()` key.
 * Other languages are loaded from `locales/<lng>/<namespace>.json` and are
 * checked against it by `scripts/check-locales.mjs` (`yarn i18n:check`).
 */
export const en = {
  common: enCommon,
  layout: enLayout,
  home: enHome,
  containers: enContainers,
  domains: enDomains,
  cron: enCron,
  users: enUsers,
  sftp: enSftp,
  hosting: enHosting,
  databases: enDatabases,
  functions: enFunctions,
  networks: enNetworks,
  apikeys: enApikeys,
  audit: enAudit,
  errors: enErrors,
} as const;

export type Namespace = keyof typeof en;

// Regional codes where variants differ (pt-PT / pt-BR) or where the script
// matters (zh-CN = Simplified Chinese). Folder names in locales/ use these codes.
export const SUPPORTED_LANGUAGES = [
  "en",
  "fr",
  "de",
  "es",
  "it",
  "nl",
  "pt-PT",
  "pt-BR",
  "ja",
  "zh-CN",
] as const;
export type Language = (typeof SUPPORTED_LANGUAGES)[number];

// Every non-English locale file, bundled eagerly (they are small).
const localeFiles = import.meta.glob<{ default: ResourceLanguage }>(
  "./locales/*/*.json",
  { eager: true },
);

function buildResources(): Resource {
  const resources: Resource = { en };
  for (const [path, mod] of Object.entries(localeFiles)) {
    const match = /\.\/locales\/([^/]+)\/([^/]+)\.json$/.exec(path);
    if (!match) continue;
    const [, lng, ns] = match;
    if (lng === "en") continue;
    (resources[lng] ??= {})[ns] = mod.default;
  }
  return resources;
}

void i18n
  .use(LanguageDetector)
  .use(initReactI18next)
  .init({
    resources: buildResources(),
    supportedLngs: SUPPORTED_LANGUAGES,
    // Load exactly the resolved code: "pt-BR" must not fall back to a "pt" bundle.
    load: "currentOnly",
    fallbackLng: {
      // Browser codes without an exact match: "fr-CA" -> "fr" is automatic;
      // these need an explicit regional target.
      pt: ["pt-PT"],
      zh: ["zh-CN"],
      "zh-Hans": ["zh-CN"],
      "zh-SG": ["zh-CN"],
      default: ["en"],
    },
    defaultNS: "common",
    ns: Object.keys(en),
    interpolation: { escapeValue: false }, // React already escapes
    detection: {
      order: ["localStorage", "navigator", "htmlTag"],
      caches: ["localStorage"],
      lookupLocalStorage: "lang",
    },
  });

const syncHtmlLang = (lng: string) => {
  document.documentElement.lang = lng;
};
syncHtmlLang(i18n.resolvedLanguage ?? "en");
i18n.on("languageChanged", syncHtmlLang);

export default i18n;
