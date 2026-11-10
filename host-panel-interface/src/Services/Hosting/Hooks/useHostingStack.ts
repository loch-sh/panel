import { useQuery } from "@tanstack/react-query";
import { useMemo } from "react";
import { isAxiosError } from "axios";
import { isSettling, SETTLE_POLL_MS } from "../actionSettle";
import { hostingService, hostingServiceKeys } from "../hosting.service";
import useSettleCleanup from "./useSettleCleanup";

const POLL_INTERVAL_MS = 2000;
const MAX_RETRIES = 2;

/** True when the API answered that the stack does not exist (or no longer). */
export function isStackNotFound(err: unknown): boolean {
  return isAxiosError(err) && err.response?.status === 404;
}

export default function useHostingStack(name: string | undefined) {
  const query = useQuery({
    queryFn: () => hostingService.get(name ?? ""),
    queryKey: [hostingServiceKeys.stack, name],
    enabled: !!name,
    retry: (count, err) => !isStackNotFound(err) && count < MAX_RETRIES,
    // Poll while an asynchronous operation (create, update, deploy, delete)
    // runs, or shortly after a start/stop/restart until the containers reach
    // the expected state. Only a 404 (stack gone) stops it: transient errors
    // keep polling.
    refetchInterval: ({ state }) => {
      if (!state.data || isStackNotFound(state.error)) return false;
      if (state.data.operation?.state === "running") return POLL_INTERVAL_MS;
      return isSettling(state.data, state.dataUpdatedAt)
        ? SETTLE_POLL_MS
        : false;
    },
  });
  const stacks = useMemo(
    () => (query.data ? [query.data] : undefined),
    [query.data],
  );
  useSettleCleanup(stacks, query.dataUpdatedAt);
  return query;
}
