/** Every IANA time zone known to the browser, UTC included. */
export function listTimezones(): string[] {
  // Intl.supportedValuesOf is ES2022 (the project targets the ES2020 lib).
  const intl = Intl as typeof Intl & {
    supportedValuesOf?: (key: "timeZone") => string[];
  };
  let zones: string[];
  try {
    zones = intl.supportedValuesOf?.("timeZone") ?? [];
  } catch {
    zones = [];
  }
  return zones.includes("UTC") ? zones : ["UTC", ...zones];
}
