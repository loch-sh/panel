import {
  keepPreviousData,
  useInfiniteQuery,
  useQueryClient,
} from "@tanstack/react-query";
import { isAxiosError } from "axios";
import { useCallback } from "react";
import { auditService, auditServiceKeys } from "../audit.service";
import { AuditFilters } from "../Models/audit.model";

/**
 * Pages of audit entries, newest first ("load more" fetches older ones).
 * Each request is itself audited: no refetch on mount or focus, and a
 * refresh restarts from the first page instead of replaying every page.
 */
export default function useAuditEntries(filters: AuditFilters, enabled = true) {
  return useInfiniteQuery({
    queryKey: [auditServiceKeys.entries, filters],
    queryFn: ({ pageParam }) => auditService.list(filters, pageParam),
    initialPageParam: undefined as number | undefined,
    getNextPageParam: (last) => last.next || undefined,
    placeholderData: keepPreviousData,
    staleTime: Infinity,
    refetchOnWindowFocus: false,
    // A refused filter (4xx) will not succeed on retry.
    retry: (count, err) =>
      count < 1 && !(isAxiosError(err) && (err.response?.status ?? 500) < 500),
    enabled,
  });
}

/** Reloads the trail from its newest entries. */
export function useAuditRefresh() {
  const client = useQueryClient();
  return useCallback(
    () => client.resetQueries({ queryKey: [auditServiceKeys.entries] }),
    [client],
  );
}
