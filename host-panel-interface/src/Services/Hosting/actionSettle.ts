import {
  HostingContainer,
  HostingStackDetail,
  HostingStackSummary,
} from "./Models/hosting.model";

/**
 * Short "settle" windows opened after a start/stop/restart/kill on a hosted
 * stack: Docker may still report the previous state right after the action
 * (and a refetch can be served before the change is visible), so the stack
 * queries poll until the containers reach the expected state, or time out.
 */

/** Poll interval while a stack settles. */
export const SETTLE_POLL_MS = 1500;
/** Polling stops after this delay even if the state never matches. */
const SETTLE_TIMEOUT_MS = 20_000;
/** Docker states a container passes through before a stable one. */
const TRANSITIONAL_STATES = new Set(["restarting", "removing"]);

export type ExpectedState = "running" | "stopped";

export interface SettleWatch {
  expect: ExpectedState;
  /** only this container of the stack (container action), else all */
  containerId?: string;
  /** data fetched before this time predates the action */
  since: number;
  timer: ReturnType<typeof setTimeout>;
}

const watches = new Map<string, SettleWatch>();
const listeners = new Set<() => void>();
let version = 0;

function emit() {
  version++;
  for (const listener of listeners) listener();
}

export function subscribeSettle(listener: () => void) {
  listeners.add(listener);
  return () => void listeners.delete(listener);
}

/** Changes on each watch added or cleared (useSyncExternalStore snapshot). */
export const getSettleVersion = () => version;

export const getSettleWatch = (name: string) => watches.get(name);

export function clearSettle(name: string) {
  const watch = watches.get(name);
  if (!watch) return;
  clearTimeout(watch.timer);
  watches.delete(name);
  emit();
}

/** Opens (or restarts) the settle window of a hosted stack. */
export function watchSettle(
  name: string,
  expect: ExpectedState,
  containerId?: string,
) {
  const previous = watches.get(name);
  if (previous) clearTimeout(previous.timer);
  const timer = setTimeout(() => clearSettle(name), SETTLE_TIMEOUT_MS);
  watches.set(name, { expect, containerId, since: Date.now(), timer });
  emit();
}

function isStable({ state, status }: HostingContainer, expect: ExpectedState) {
  if (TRANSITIONAL_STATES.has(state)) return false;
  if (expect === "stopped") return state !== "running";
  // A one-shot container (init, migration) legitimately exits with code 0.
  return (
    state === "running" ||
    (state === "exited" && status.startsWith("Exited (0)"))
  );
}

/** True when the stack (detail or list summary) shows the expected state. */
export function isSettled(
  watch: SettleWatch,
  stack: HostingStackSummary | HostingStackDetail,
) {
  if ("containers" in stack) {
    return (stack.containers ?? [])
      .filter((c) => !watch.containerId || c.id === watch.containerId)
      .every((c) => isStable(c, watch.expect));
  }
  // The list only has counters, which cannot tell one container's state
  // (others may be stopped on purpose): the detail query or the timeout
  // closes a container-scoped window.
  if (watch.containerId) return true;
  return watch.expect === "running"
    ? stack.running === stack.total
    : stack.running === 0;
}

/** True while the stack is in its settle window and not settled yet. */
export function isSettling(
  stack: HostingStackSummary | HostingStackDetail,
  dataUpdatedAt: number,
) {
  const watch = watches.get(stack.name);
  if (!watch) return false;
  return !(dataUpdatedAt > watch.since && isSettled(watch, stack));
}
