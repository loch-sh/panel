export type AuditLevel = "INFO" | "WARN" | "ERROR";

export const AUDIT_LEVELS: readonly AuditLevel[] = ["INFO", "WARN", "ERROR"];

export function isAuditLevel(level: string): level is AuditLevel {
  return (AUDIT_LEVELS as readonly string[]).includes(level);
}

export interface AuditEntryModel {
  /** Offset of the entry in the log: stable and unique. */
  id: number;
  time: string;
  level: AuditLevel | string;
  event: string;
  user?: string;
  fields?: Record<string, unknown>;
}

export interface AuditPageModel {
  entries: AuditEntryModel[];
  /** Cursor of the next (older) page; absent at the start of the log. */
  next?: number;
}

export interface AuditFilters {
  from?: string; // RFC 3339
  to?: string; // RFC 3339, exclusive
  user?: string;
  events?: string[];
  /** Events hidden from the results (e.g. the trail's own consultations). */
  excludeEvents?: string[];
  levels?: AuditLevel[];
  q?: string;
}

/** Categories of audit events, in display order. */
export const AUDIT_CATEGORIES = [
  "auth",
  "security",
  "containers",
  "cron",
  "users",
  "apikeys",
  "sftp",
  "hosting",
  "services",
  "networks",
  "functions",
] as const;

export type AuditCategory = (typeof AUDIT_CATEGORIES)[number];

/**
 * Events written by the API (the `msg` of each audit record), with their
 * category. Unknown events are still listed, under their raw name.
 */
export const AUDIT_EVENTS = {
  "user logged in": "auth",
  "user logged in but email could not be extracted from ID token": "auth",
  "user logged out": "auth",
  "login failed": "auth",
  "user auto-provisioned": "auth",
  "api request": "security",
  "access denied": "security",
  "audit trail viewed": "security",
  "container action": "containers",
  "stack action": "containers",
  "shell attached": "containers",
  "logs stream started": "containers",
  "logs viewed": "containers",
  "logs downloaded": "containers",
  "file tail": "containers",
  "proxy session started": "containers",
  "stats stream started": "containers",
  "cp download": "containers",
  "cp upload": "containers",
  "cron job created": "cron",
  "cron job updated": "cron",
  "cron job updated (patch)": "cron",
  "cron job deleted": "cron",
  "manual cron job execution": "cron",
  "cron job completed": "cron",
  "cron job failed": "cron",
  "user profile toggled": "users",
  "user restricted": "users",
  "permissions updated": "users",
  "user deleted": "users",
  "api key created": "apikeys",
  "api key revoked": "apikeys",
  "sftp user created": "sftp",
  "sftp user updated": "sftp",
  "sftp user deleted": "sftp",
  "sftp user password reset": "sftp",
  "sftp user status changed": "sftp",
  "hosting stack created": "hosting",
  "hosting stack updated": "hosting",
  "hosting stack deploy": "hosting",
  "hosting stack delete": "hosting",
  "hosting operation finished": "hosting",
  "service instance created": "services",
  "service instance retired": "services",
  "service instance purged": "services",
  "service instance purge incomplete": "services",
  "service instance updated": "services",
  "service instance update rolled back": "services",
  "service exposure applied": "services",
  "service exposure changed": "services",
  "service exposure revoked by policy": "services",
  "service instance networks updated": "services",
  "service instance networks update failed": "services",
  "service resource created": "services",
  "service resource deleted": "services",
  "service resource password reset": "services",
  "service resource updated": "services",
  "service resource update rolled back": "services",
  "network created": "networks",
  "network updated": "networks",
  "network deleted": "networks",
  "network recreated": "networks",
  "network compromised": "networks",
  "network member attached": "networks",
  "network member detached": "networks",
  "network container disconnected": "networks",
  "function created": "functions",
  "function updated": "functions",
  "function deleted": "functions",
  "function purged": "functions",
  "function deletion incomplete": "functions",
  "function code deployed": "functions",
  "function code deployment failed": "functions",
  "function deployment activated": "functions",
  "function deployment rolled back": "functions",
  "function secret set": "functions",
  "function secret removed": "functions",
  "function credentials rotated": "functions",
  "function network attached": "functions",
  "function network detached": "functions",
  "function egress toggled": "functions",
  "function action": "functions",
  "function verification refused": "functions",
  "function runtime installed": "functions",
  "function runtime refused": "functions",
  "function invoked": "functions",
} as const satisfies Record<string, AuditCategory>;

export type AuditEvent = keyof typeof AUDIT_EVENTS;

export function isKnownAuditEvent(event: string): event is AuditEvent {
  return Object.prototype.hasOwnProperty.call(AUDIT_EVENTS, event);
}

/** Translation key of an event (namespace `audit`, under `events`). */
export function auditEventKey(event: AuditEvent): string {
  return event.replace(/[^a-z0-9]+/gi, "_").replace(/^_|_$/g, "");
}
