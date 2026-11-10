import { useQuery } from "@tanstack/react-query";
import { functionsService, functionsServiceKeys } from "../functions.service";

/** `enabled`: false for non-admins, the endpoint would answer 403. */
export default function useFunctionsConfig(enabled = true) {
  return useQuery({
    enabled,
    queryFn: functionsService.config,
    queryKey: [functionsServiceKeys.config],
    // count and used resources change with the functions.
    staleTime: 30_000,
    // Restricted users get 403 on this admin endpoint; don't retry.
    retry: false,
  });
}
