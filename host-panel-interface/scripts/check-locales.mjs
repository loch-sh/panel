#!/usr/bin/env node
// Checks that every locale has exactly the same keys and interpolation
// placeholders ({{name}}) as English, the source of truth.
import { readdirSync, readFileSync } from "node:fs";
import { join } from "node:path";

const root = new URL("../src/i18n/locales/", import.meta.url).pathname;
const SOURCE = "en";

const flatten = (obj, prefix = "") =>
  Object.entries(obj).flatMap(([k, v]) =>
    v && typeof v === "object"
      ? flatten(v, `${prefix}${k}.`)
      : [[`${prefix}${k}`, String(v)]],
  );

// Interpolations ({{name}}) and <Trans> component tags (<code>…</code>, <br/>) must match.
const placeholders = (s) =>
  [
    ...[...s.matchAll(/{{\s*([\w.]+)\s*}}/g)].map((m) => m[1]),
    ...[...s.matchAll(/<\/?([\w]+)\s*\/?>/g)].map((m) => `<${m[0].startsWith("</") ? "/" : ""}${m[1]}>`),
  ]
    .sort()
    .join(",");

// "{{count}}" may be omitted in some plural forms ("one item"), ignore it there.
const withoutCount = (s, plural) =>
  placeholders(s).split(",").filter((p) => p && !(plural && p === "count")).join(",");

const load = (lng, ns) => new Map(flatten(JSON.parse(readFileSync(join(root, lng, ns), "utf8"))));

const languages = readdirSync(root).filter((l) => l !== SOURCE);
const namespaces = readdirSync(join(root, SOURCE)).filter((f) => f.endsWith(".json"));
const errors = [];

// Every language declared in SUPPORTED_LANGUAGES must have a locale folder, and vice versa.
const indexSrc = readFileSync(new URL("../src/i18n/index.ts", import.meta.url), "utf8");
const declared = [...(/SUPPORTED_LANGUAGES = \[([^\]]*)\]/.exec(indexSrc)?.[1] ?? "").matchAll(/"([^"]+)"/g)].map((m) => m[1]);
const folders = [SOURCE, ...languages];
for (const lng of declared) if (!folders.includes(lng)) errors.push(`${lng}: declared in SUPPORTED_LANGUAGES but src/i18n/locales/${lng}/ is missing`);
for (const lng of folders) if (!declared.includes(lng)) errors.push(`${lng}: locale folder exists but is not in SUPPORTED_LANGUAGES`);

// Plural forms differ per language (fr/es/it also have "_many"), so plural
// keys are compared on their base name and must at least define "_other".
const PLURAL = /_(zero|one|two|few|many|other)$/;
const groupPlurals = (map) => {
  const out = new Map();
  for (const [key, value] of map) {
    const base = key.replace(PLURAL, "");
    const entry = out.get(base) ?? { forms: new Map(), plural: false };
    entry.plural ||= PLURAL.test(key);
    entry.forms.set(key, value);
    out.set(base, entry);
  }
  return out;
};

for (const ns of namespaces) {
  const source = groupPlurals(load(SOURCE, ns));
  for (const lng of languages) {
    let target;
    try {
      target = groupPlurals(load(lng, ns));
    } catch (e) {
      errors.push(`${lng}/${ns}: ${e.message}`);
      continue;
    }
    for (const [base, src] of source) {
      const tgt = target.get(base);
      if (!tgt) {
        errors.push(`${lng}/${ns}: missing "${base}"`);
        continue;
      }
      if (src.plural && !tgt.forms.has(`${base}_other`))
        errors.push(`${lng}/${ns}: plural "${base}" has no "_other" form`);
      const refValue = src.forms.get(`${base}_other`) ?? [...src.forms.values()][0];
      for (const [key, value] of tgt.forms) {
        if (!value.trim()) errors.push(`${lng}/${ns}: empty "${key}"`);
        else if (withoutCount(value, src.plural) !== withoutCount(refValue, src.plural))
          errors.push(`${lng}/${ns}: placeholders differ for "${key}"`);
      }
    }
    for (const base of target.keys())
      if (!source.has(base)) errors.push(`${lng}/${ns}: extra "${base}" (not in ${SOURCE})`);
  }
}

if (errors.length) {
  console.error(errors.join("\n"));
  console.error(`\n${errors.length} locale error(s)`);
  process.exit(1);
}
console.log(`Locales OK: ${[SOURCE, ...languages].join(", ")} × ${namespaces.length} namespaces`);
