import { useQuery } from "@tanstack/react-query";
import { databasesService, databasesServiceKeys } from "../databases.service";

/** `enabled`: false for non-admins, the endpoint would answer 403. */
export default function useServicesConfig(enabled = true) {
  return useQuery({
    enabled,
    queryFn: databasesService.config,
    queryKey: [databasesServiceKeys.config],
    staleTime: Infinity,
    // Restricted users get 403 on this admin endpoint; don't retry.
    retry: false,
  });
}
