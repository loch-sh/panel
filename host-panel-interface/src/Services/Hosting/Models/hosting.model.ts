export const HOSTING_TEMPLATES = [
  "php-nginx",
  "php-apache",
  "nginx",
  "apache",
] as const;

export type HostingTemplate = (typeof HOSTING_TEMPLATES)[number];

/** Templates running PHP (they accept a PHP version, settings and php.ini). */
export function templateHasPhp(template?: HostingTemplate): boolean {
  return template === "php-nginx" || template === "php-apache";
}

export interface PhpVersionModel {
  tag: string;
  /** End of active support (bug fixes) of the PHP branch, YYYY-MM-DD. */
  activeSupportUntil?: string;
  /** End of security support of the PHP branch, YYYY-MM-DD. */
  securitySupportUntil?: string;
  /** True once security support has ended. */
  deprecated: boolean;
}

export interface PhpSettings {
  /**
   * PHP CLI (crons, console) memory limit, also the default for web
   * requests: e.g. "256M", or "-1" for unlimited.
   */
  memoryLimit: string;
  /**
   * Web requests only (PHP-FPM pool, the app cannot raise it). "" = same as
   * memoryLimit; absent on older stacks.
   */
  webMemoryLimit?: string;
  uploadMaxFilesize: string;
  /** must be >= uploadMaxFilesize */
  postMaxSize: string;
  /** seconds, 0-3600; web requests only (the CLI is never time-limited) */
  maxExecutionTime: number;
  displayErrors: boolean;
  /** IANA time zone */
  timezone: string;
  /**
   * Modules switched off (names from GET /hosting/php-modules). Absent or
   * null on stacks created before the option existed.
   */
  disabledModules?: string[] | null;
}

/** PHP module available in an image. */
export interface PhpModuleModel {
  name: string;
  /** ini file name in conf.d, e.g. "docker-php-ext-pdo_pgsql.ini" */
  file: string;
}

export interface PhpModulesResponse {
  modules: PhpModuleModel[];
}

export interface HostingResources {
  memoryMB: number;
  cpus: number;
}

export interface HostingLimits {
  maxMemoryMB: number;
  maxCpus: number;
  maxDomains: number;
  maxEnvVars: number;
  maxSnippetBytes: number;
}

export interface HostingDefaults {
  phpVersion: string;
  documentRoot: string;
  php: PhpSettings;
  resources: HostingResources;
}

export interface HostingConfigModel {
  enabled: boolean;
  sitesDirectory?: string;
  templates?: HostingTemplate[];
  phpVersions?: PhpVersionModel[];
  defaults?: HostingDefaults;
  limits?: HostingLimits;
  /** networks preselected for a new stack (["services"] with the services module) */
  defaultNetworks?: string[];
}

export interface HostingSiteModel {
  name: string;
  /** hosted stack using this directory, if any */
  stack?: string;
}

export interface EnvVar {
  name: string;
  value: string;
}

/** PHP framework whose routing rules the API generates. */
export type Framework = "symfony" | "laravel";

/** "" = routing of the image; a framework needs a PHP template. */
export type RoutingMode = "" | Framework | "custom";

export interface HostingSpec {
  name: string;
  template: HostingTemplate;
  phpVersion?: string;
  site: string;
  /** relative path inside the site; "" = site root */
  documentRoot: string;
  domains: string[];
  https: boolean;
  redirectHttps: boolean;
  php?: PhpSettings;
  resources: HostingResources;
  env: EnvVar[];
  routing?: RoutingMode;
  /** "symfony" only: dirs inside the document root served as static files */
  assetDirs?: string[] | null;
  /** private networks the stack joins (registry names, sorted by the API) */
  networks?: string[];
}

export interface HostingSnippets {
  phpIni: string;
  vhost: string;
  /** routing "custom" only ("" otherwise) */
  routingRules?: string;
}

export interface RoutingTemplateResponse {
  rules: string;
}

export type OperationKind = "create" | "update" | "deploy" | "delete";
export type OperationState = "running" | "succeeded" | "failed";

export interface HostingOperation {
  id: string;
  kind: OperationKind;
  state: OperationState;
  /** HOSTING_{CREATE,UPDATE,DEPLOY,DELETE}_FAILED or HOSTING_OPERATION_INTERRUPTED */
  error?: string;
  rolledBack?: boolean;
  /** docker compose output (detail only, not in the stack list) */
  output?: string;
  startedAt: string;
  finishedAt?: string;
}

export type HostingStackStatus =
  "running" | "partial" | "stopped" | "not_deployed";

export interface HostingStackSummary {
  name: string;
  template: HostingTemplate;
  phpVersion?: string;
  image: string;
  site: string;
  domains: string[];
  https: boolean;
  redirectHttps: boolean;
  /** real compose project ("hosted-<name>"), used for the stack actions */
  project: string;
  status: HostingStackStatus;
  running: number;
  total: number;
  /** compose file edited outside the panel */
  modified: boolean;
  operation?: HostingOperation | null;
}

export interface HostingContainer {
  id: string;
  name: string;
  state: string;
  status: string;
  image: string;
}

export interface HostingStackDetail extends HostingStackSummary {
  /** null when the compose file cannot be read back */
  spec: HostingSpec | null;
  snippets: HostingSnippets;
  containers: HostingContainer[];
  /**
   * Env values and snippets emptied (read-only API keys; never set for the
   * browser session). The settings must then not be saved back.
   */
  secretsMasked?: boolean;
  /** The compose.yaml on disk (empty when secrets are masked). */
  compose?: string;
  /** routing file in use (generated or custom rules); "" = image default */
  routingFile?: string;
}

/** Body of POST /hosting/stacks and PUT /hosting/stacks/{name}. */
export type HostingStackPayload = HostingSpec & HostingSnippets;

export interface HostingOperationResponse {
  name: string;
  operation: HostingOperation | null;
}

export type NameTakenReason =
  | "reserved"
  | "stack"
  | "directory"
  | "compose_project"
  | "container"
  | "traefik_router";

export interface CheckNameResponse {
  available: boolean;
  code?: "INVALID_HOSTING_NAME" | "HOSTING_NAME_TAKEN";
  reason?: NameTakenReason;
}

/** Stack names: 1-32 lowercase letters, digits or dashes, no leading/trailing dash. */
export const HOSTING_NAME_PATTERN = /^[a-z0-9]([a-z0-9-]*[a-z0-9])?$/;
export const HOSTING_NAME_MAX_LENGTH = 32;
