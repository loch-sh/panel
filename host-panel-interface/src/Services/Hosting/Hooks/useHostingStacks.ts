import { useQuery } from "@tanstack/react-query";
import { isSettling, SETTLE_POLL_MS } from "../actionSettle";
import { hostingService, hostingServiceKeys } from "../hosting.service";
import useSettleCleanup from "./useSettleCleanup";

const POLL_INTERVAL_MS = 2000;

export default function useHostingStacks(enabled = true) {
  const query = useQuery({
    queryFn: hostingService.listStacks,
    queryKey: [hostingServiceKeys.stacks],
    enabled,
    // Poll while an operation is running on any stack, or shortly after a
    // start/stop/restart until the stack reaches the expected state.
    refetchInterval: ({ state }) => {
      const stacks = state.data ?? [];
      if (stacks.some((s) => s.operation?.state === "running")) {
        return POLL_INTERVAL_MS;
      }
      return stacks.some((s) => isSettling(s, state.dataUpdatedAt))
        ? SETTLE_POLL_MS
        : false;
    },
  });
  useSettleCleanup(query.data, query.dataUpdatedAt);
  return query;
}
