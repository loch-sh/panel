import type { TFunction } from "i18next";
import { API_KEY_SCOPES, ApiKeyScope } from "./Models/apikeys.model";

/** Translation key of each API scope label (namespace `apikeys`). */
const SCOPE_LABEL_KEYS = {
  "*": "scopes.all",
  "containers:read": "scopes.containersRead",
  "containers:logs": "scopes.containersLogs",
  "containers:actions": "scopes.containersActions",
  "containers:shell": "scopes.containersShell",
  "containers:stats": "scopes.containersStats",
  "containers:proxy": "scopes.containersProxy",
  "containers:cp": "scopes.containersCp",
  cron: "scopes.cron",
  "sftp:read": "scopes.sftpRead",
  "sftp:write": "scopes.sftpWrite",
  "hosting:read": "scopes.hostingRead",
  "hosting:write": "scopes.hostingWrite",
  "networks:read": "scopes.networksRead",
  "networks:write": "scopes.networksWrite",
  "services:read": "scopes.servicesRead",
  "services:write": "scopes.servicesWrite",
  "services:expose": "scopes.servicesExpose",
  "audit:read": "scopes.auditRead",
} as const satisfies Record<ApiKeyScope, string>;

function isKnownScope(scope: string): scope is ApiKeyScope {
  return (API_KEY_SCOPES as readonly string[]).includes(scope);
}

/** Human-readable label of an API scope; unknown scopes are shown as is. */
export function apiKeyScopeLabel(
  t: TFunction<"apikeys">,
  scope: string,
): string {
  return isKnownScope(scope) ? t(SCOPE_LABEL_KEYS[scope]) : scope;
}
