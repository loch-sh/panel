import {
  EnvVar,
  Framework,
  HostingConfigModel,
  HostingSnippets,
  HostingSpec,
  HostingStackPayload,
  HostingTemplate,
  PhpSettings,
  RoutingMode,
  templateHasPhp,
} from "../../../Services/Hosting/Models/hosting.model";
import { normalizeAssetDirs, normalizePath } from "./formPaths";

export type SiteMode = "new" | "existing";

/** Values of the stack form (create and edit). */
export interface HostingFormValues {
  template: HostingTemplate;
  name: string;
  siteMode: SiteMode;
  /** existing site directory (siteMode "existing", or edit mode) */
  site?: string;
  documentRoot: string;
  domains: string[];
  https: boolean;
  redirectHttps: boolean;
  /** private networks the site joins (registry names) */
  networks: string[];
  phpVersion?: string;
  resources: { memoryMB: number; cpus: number };
  php?: PhpSettings;
  env: EnvVar[];
  phpIni: string;
  vhost: string;
  /** framework routing ("" = none; PHP templates only) */
  framework: "" | Framework;
  assetDirs: string[];
  /** routing "custom": takes precedence over `framework` */
  customRouting: boolean;
  routingRules: string;
}

/** Asset directories suggested for Symfony routing. */
export const DEFAULT_ASSET_DIRS = ["bundles"];

const FALLBACK_PHP: PhpSettings = {
  memoryLimit: "256M",
  uploadMaxFilesize: "64M",
  postMaxSize: "64M",
  maxExecutionTime: 60,
  displayErrors: false,
  timezone: "UTC",
  disabledModules: ["ioncube"],
};

/** Time zone of the browser, if the platform reports one. */
function browserTimezone(): string | undefined {
  try {
    return Intl.DateTimeFormat().resolvedOptions().timeZone || undefined;
  } catch {
    return undefined;
  }
}

/** Initial values of the creation form, from the server defaults. */
export function defaultFormValues(
  config: HostingConfigModel,
): HostingFormValues {
  const defaults = config.defaults;
  const php = defaults?.php ?? FALLBACK_PHP;
  return {
    template: config.templates?.[0] ?? "php-nginx",
    name: "",
    siteMode: "new",
    documentRoot: defaults?.documentRoot ?? "web",
    domains: [],
    https: false,
    redirectHttps: false,
    // The shared network when the services module is enabled.
    networks: config.defaultNetworks ?? [],
    phpVersion: defaults?.phpVersion,
    // 0 = no limit (the server default).
    resources: defaults?.resources ?? { memoryMB: 0, cpus: 0 },
    php: {
      ...php,
      // The server default wins when an admin configured one; "UTC" is the
      // built-in default, then the browser's zone is a better guess.
      timezone:
        php.timezone && php.timezone !== "UTC"
          ? php.timezone
          : (browserTimezone() ?? php.timezone),
      webMemoryLimit: php.webMemoryLimit ?? "",
      disabledModules: php.disabledModules ?? [],
    },
    env: [],
    phpIni: "",
    vhost: "",
    framework: "",
    assetDirs: DEFAULT_ASSET_DIRS,
    customRouting: false,
    routingRules: "",
  };
}

/** Form values of an existing stack. */
export function specToFormValues(
  spec: HostingSpec,
  snippets: HostingSnippets,
  config: HostingConfigModel,
): HostingFormValues {
  const defaults = defaultFormValues(config);
  return {
    template: spec.template,
    name: spec.name,
    siteMode: "existing",
    site: spec.site,
    documentRoot: spec.documentRoot,
    domains: spec.domains ?? [],
    https: spec.https,
    redirectHttps: spec.redirectHttps,
    networks: spec.networks ?? [],
    phpVersion: spec.phpVersion || defaults.phpVersion,
    resources: {
      memoryMB: spec.resources?.memoryMB ?? 0,
      cpus: spec.resources?.cpus ?? 0,
    },
    php: spec.php
      ? {
          ...spec.php,
          webMemoryLimit: spec.php.webMemoryLimit ?? "",
          disabledModules: spec.php.disabledModules ?? [],
        }
      : defaults.php,
    env: spec.env ?? [],
    phpIni: snippets.phpIni ?? "",
    vhost: snippets.vhost ?? "",
    framework:
      spec.routing === "symfony" || spec.routing === "laravel"
        ? spec.routing
        : "",
    assetDirs: spec.assetDirs?.length ? spec.assetDirs : DEFAULT_ASSET_DIRS,
    customRouting: spec.routing === "custom",
    routingRules: snippets.routingRules ?? "",
  };
}

/** Routing mode sent to the API (a framework needs a PHP template). */
export function routingOf(values: HostingFormValues): RoutingMode {
  if (values.customRouting) return "custom";
  return values.framework && templateHasPhp(values.template)
    ? values.framework
    : "";
}

/** Request body built from the form values (PHP fields only for PHP templates). */
export function formValuesToPayload(
  values: HostingFormValues,
): HostingStackPayload {
  const hasPhp = templateHasPhp(values.template);
  const name = values.name.trim();
  const routing = routingOf(values);
  return {
    name,
    template: values.template,
    phpVersion: hasPhp ? values.phpVersion : undefined,
    site: values.siteMode === "new" ? name : (values.site ?? "").trim() || name,
    documentRoot: normalizePath(values.documentRoot),
    domains: (values.domains ?? []).map((d) => d.trim().toLowerCase()),
    https: values.https,
    redirectHttps: values.https && values.redirectHttps,
    networks: [...(values.networks ?? [])].sort(),
    php:
      hasPhp && values.php
        ? {
            ...values.php,
            memoryLimit: values.php.memoryLimit.trim(),
            uploadMaxFilesize: values.php.uploadMaxFilesize.trim(),
            postMaxSize: values.php.postMaxSize.trim(),
            // "" = same as memoryLimit (always sent).
            webMemoryLimit: (values.php.webMemoryLimit ?? "").trim(),
            disabledModules: values.php.disabledModules ?? [],
          }
        : undefined,
    // A cleared input gives null: 0 means "no limit".
    resources: {
      memoryMB: values.resources?.memoryMB ?? 0,
      cpus: values.resources?.cpus ?? 0,
    },
    env: (values.env ?? []).map(({ name: key, value }) => ({
      name: key.trim(),
      value: value ?? "",
    })),
    phpIni: hasPhp ? (values.phpIni ?? "") : "",
    vhost: values.vhost ?? "",
    routing,
    // Asset dirs only with Symfony, rules only when custom (API rules).
    assetDirs:
      routing === "symfony" ? normalizeAssetDirs(values.assetDirs) : [],
    routingRules: routing === "custom" ? (values.routingRules ?? "") : "",
  };
}
