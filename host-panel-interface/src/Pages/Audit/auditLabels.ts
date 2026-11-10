import type { TFunction } from "i18next";
import {
  auditEventKey,
  isAuditLevel,
  isKnownAuditEvent,
} from "../../Services/Audit/Models/audit.model";

/** Translated label of an event; unknown events are shown as is. */
export function auditEventLabel(t: TFunction<"audit">, event: string): string {
  return isKnownAuditEvent(event)
    ? t(`events.${auditEventKey(event)}` as never)
    : event;
}

/** Translated label of a level; unknown levels are shown as is. */
export function auditLevelLabel(t: TFunction<"audit">, level: string): string {
  return isAuditLevel(level) ? t(`levels.${level}`) : level;
}
