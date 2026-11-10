import type { Locale as DateFnsLocale } from "date-fns";
import { de, enUS, es, fr, it, ja, nl, pt, ptBR, zhCN } from "date-fns/locale";
import type { Locale as AntdLocale } from "antd/es/locale";
import antdDeDE from "antd/locale/de_DE";
import antdEnUS from "antd/locale/en_US";
import antdEsES from "antd/locale/es_ES";
import antdFrFR from "antd/locale/fr_FR";
import antdItIT from "antd/locale/it_IT";
import antdJaJP from "antd/locale/ja_JP";
import antdNlNL from "antd/locale/nl_NL";
import antdPtBR from "antd/locale/pt_BR";
import antdPtPT from "antd/locale/pt_PT";
import antdZhCN from "antd/locale/zh_CN";
import { useTranslation } from "react-i18next";
import { Language, SUPPORTED_LANGUAGES } from ".";

const dateFnsLocales: Record<Language, DateFnsLocale> = {
  en: enUS,
  fr,
  de,
  es,
  it,
  nl,
  "pt-PT": pt,
  "pt-BR": ptBR,
  ja,
  "zh-CN": zhCN,
};

const antdLocales: Record<Language, AntdLocale> = {
  en: antdEnUS,
  fr: antdFrFR,
  de: antdDeDE,
  es: antdEsES,
  it: antdItIT,
  nl: antdNlNL,
  "pt-PT": antdPtPT,
  "pt-BR": antdPtBR,
  ja: antdJaJP,
  "zh-CN": antdZhCN,
};

/** cronstrue uses underscores for regional codes (pt_BR, zh_CN). */
const cronstrueLocales: Record<Language, string> = {
  en: "en",
  fr: "fr",
  de: "de",
  es: "es",
  it: "it",
  nl: "nl",
  "pt-PT": "pt_PT",
  "pt-BR": "pt_BR",
  ja: "ja",
  "zh-CN": "zh_CN",
};

/** Current UI language, re-rendering on change. */
export function useLanguage(): Language {
  const { i18n } = useTranslation();
  const lng = i18n.resolvedLanguage as Language | undefined;
  return lng && SUPPORTED_LANGUAGES.includes(lng) ? lng : "en";
}

/** date-fns locale matching the UI language (pass to format / formatDistance). */
export function useDateFnsLocale(): DateFnsLocale {
  return dateFnsLocales[useLanguage()];
}

/** antd locale matching the UI language (for ConfigProvider). */
export function useAntdLocale(): AntdLocale {
  return antdLocales[useLanguage()];
}

/** cronstrue locale code matching the UI language. */
export function useCronstrueLocale(): string {
  return cronstrueLocales[useLanguage()];
}
