import { useEffect, useSyncExternalStore } from "react";
import {
  clearSettle,
  getSettleVersion,
  getSettleWatch,
  isSettled,
  subscribeSettle,
} from "../actionSettle";
import {
  HostingStackDetail,
  HostingStackSummary,
} from "../Models/hosting.model";

/**
 * Closes the settle window of the given stacks once data fetched after the
 * action shows the expected state (stops the polling and the indicator).
 */
export default function useSettleCleanup(
  stacks: readonly (HostingStackSummary | HostingStackDetail)[] | undefined,
  dataUpdatedAt: number,
) {
  // Re-checks when a window opens on data that is already up to date.
  const version = useSyncExternalStore(subscribeSettle, getSettleVersion);

  useEffect(() => {
    // Windows are shared: the list or the detail query may close them.
    for (const stack of stacks ?? []) {
      const watch = getSettleWatch(stack.name);
      if (watch && dataUpdatedAt > watch.since && isSettled(watch, stack)) {
        clearSettle(stack.name);
      }
    }
  }, [stacks, dataUpdatedAt, version]);
}
