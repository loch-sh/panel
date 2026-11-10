import { useQueryClient } from "@tanstack/react-query";
import { useCallback, useEffect, useRef } from "react";
import { invalidateAfterOperation } from "../invalidateAfterOperation";
import { HostingOperation } from "../Models/hosting.model";

export interface WatchedStack {
  name: string;
  operation?: HostingOperation | null;
}

/**
 * Called when an operation seen running has finished. `operation` is null
 * when the stack disappeared meanwhile (a delete operation succeeded).
 */
export type OperationFinishedHandler = (
  name: string,
  operation: HostingOperation | null,
) => void;

/**
 * Tracks the running operations of the given stacks (list or detail page).
 * When one finishes, or its stack disappears, the related data is refreshed
 * and the caller is notified. Operations already finished when first seen are
 * ignored, unless announced with the returned `track` (an operation started
 * from the page may end before the next poll shows it running).
 */
export default function useHostingOperationWatcher(
  stacks: readonly WatchedStack[] | undefined,
  onFinished?: OperationFinishedHandler,
) {
  const client = useQueryClient();
  // Stack name -> id of the operation seen running.
  const running = useRef(new Map<string, string>());
  const callback = useRef(onFinished);

  useEffect(() => {
    callback.current = onFinished;
  }, [onFinished]);

  useEffect(() => {
    if (!stacks) return;
    const tracked = running.current;
    const finished: [string, HostingOperation | null][] = [];
    const present = new Set(stacks.map((s) => s.name));

    for (const { name, operation } of stacks) {
      if (operation?.state === "running") {
        tracked.set(name, operation.id);
      } else if (operation && tracked.get(name) === operation.id) {
        tracked.delete(name);
        finished.push([name, operation]);
      }
    }
    for (const name of [...tracked.keys()]) {
      if (!present.has(name)) {
        tracked.delete(name);
        finished.push([name, null]);
      }
    }

    if (finished.length === 0) return;
    invalidateAfterOperation(client);
    for (const [name, op] of finished) callback.current?.(name, op);
  }, [client, stacks]);

  return useCallback((name: string, operationId: string) => {
    running.current.set(name, operationId);
  }, []);
}
