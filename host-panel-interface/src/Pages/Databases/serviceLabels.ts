import type { TFunction } from "i18next";
import {
  isServiceEngine,
  SIZE_OPTIONS,
} from "../../Services/Databases/Models/databases.model";

/** Display name of an engine (brand names are translated keys too). */
export function engineLabel(t: TFunction<"databases">, id: string): string {
  return isServiceEngine(id) ? t(`engines.${id}`) : id;
}

/** A size in MB, shown as 256 MB or 1 GB in the UI language. */
export function formatSize(mb: number, lang: string | undefined): string {
  if (mb >= 1024 * 1024 && mb % (1024 * 1024) === 0) {
    return new Intl.NumberFormat(lang, {
      style: "unit",
      unit: "terabyte",
      unitDisplay: "short",
    }).format(mb / (1024 * 1024));
  }
  if (mb >= 1024) {
    return new Intl.NumberFormat(lang, {
      style: "unit",
      unit: "gigabyte",
      unitDisplay: "short",
      maximumFractionDigits: 3,
    }).format(mb / 1024);
  }
  return new Intl.NumberFormat(lang, {
    style: "unit",
    unit: "megabyte",
    unitDisplay: "short",
  }).format(mb);
}

/** Option keys and values the UI knows (others are shown as is). */
const KNOWN_VALUES: Record<string, readonly string[]> = {
  policy: [
    "noeviction",
    "allkeys-lru",
    "allkeys-lfu",
    "volatile-lru",
    "volatile-lfu",
    "volatile-ttl",
  ],
  persistence: ["aof", "rdb", "none", "on", "off"],
  scripting: ["on", "off"],
  encryption: ["off", "on"],
  limits: ["strict", "relaxed"],
  modules: ["off", "on"],
};

export const KNOWN_OPTIONS = [
  "memory",
  "storage",
  "quota",
  "policy",
  "persistence",
  "scripting",
  "encryption",
  "message-size",
  "payload",
  "queued",
  "limits",
  "modules",
] as const;
type KnownOption = (typeof KNOWN_OPTIONS)[number];

export function isKnownOption(key: string): key is KnownOption {
  return (KNOWN_OPTIONS as readonly string[]).includes(key);
}

/** Label of an option value ("256 MB", "LRU on all keys"...). */
export function optionValueLabel(
  t: TFunction<"databases">,
  key: string,
  value: string,
  lang: string | undefined,
): string {
  if (SIZE_OPTIONS.includes(key)) {
    const mb = Number(value);
    return Number.isFinite(mb) ? formatSize(mb, lang) : value;
  }
  if (KNOWN_VALUES[key]?.includes(value)) {
    return t(`options.${key}.values.${value}.label` as never);
  }
  // Counts (e.g. queued messages): grouped digits in the UI language.
  if (/^[0-9]+$/.test(value)) {
    return new Intl.NumberFormat(lang).format(Number(value));
  }
  return value;
}

/** Description of an option value, when the UI has one. */
export function optionValueDescription(
  t: TFunction<"databases">,
  key: string,
  value: string,
): string | undefined {
  if (KNOWN_VALUES[key]?.includes(value)) {
    return t(`options.${key}.values.${value}.description` as never);
  }
  return undefined;
}

/** Label of an option (its key when the UI does not know it). */
export function optionLabel(t: TFunction<"databases">, key: string): string {
  return isKnownOption(key) ? t(`options.${key}.label`) : key;
}

/** Help of an option, when the UI knows it. */
export function optionHelp(
  t: TFunction<"databases">,
  key: string,
): string | undefined {
  return isKnownOption(key) ? t(`options.${key}.help`) : undefined;
}
