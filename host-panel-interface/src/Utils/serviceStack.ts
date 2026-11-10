import { COMPOSE_PROJECT_LABEL } from "./hostedStack";
import {
  isServiceEngine,
  type ServiceEngineId,
} from "../Services/Databases/Models/databases.model";

/** Prefix of the compose projects of the services module ("svc-redis-cache"). */
export const SERVICE_STACK_PREFIX = "svc-";
/** Labels set by the API on the container of a service instance. */
export const SERVICE_ENGINE_LABEL = "sh.loch.service";
export const SERVICE_INSTANCE_LABEL = "sh.loch.instance";

/**
 * Engine of a container of a managed service instance (database, cache,
 * messaging...), or null: project prefixed with "svc-", carrying the
 * instance and engine labels the API renders. A display hint only (labels
 * can be set by any stack): the API decides what it manages.
 */
export function serviceEngineOf(
  labels: Record<string, string> | undefined | null,
): ServiceEngineId | null {
  if (!labels) return null;
  const project = labels[COMPOSE_PROJECT_LABEL] ?? "";
  const engine = labels[SERVICE_ENGINE_LABEL];
  if (
    !project.startsWith(SERVICE_STACK_PREFIX) ||
    !labels[SERVICE_INSTANCE_LABEL] ||
    !isServiceEngine(engine)
  ) {
    return null;
  }
  return engine;
}
