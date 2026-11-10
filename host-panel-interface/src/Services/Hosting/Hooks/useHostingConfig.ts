import { useQuery } from "@tanstack/react-query";
import { hostingService, hostingServiceKeys } from "../hosting.service";

/** `enabled`: false for non-admins, the endpoint would answer 403. */
export default function useHostingConfig(enabled = true) {
  return useQuery({
    enabled,
    queryFn: hostingService.config,
    queryKey: [hostingServiceKeys.config],
    staleTime: Infinity,
    // Restricted users get 403 on this admin endpoint; don't retry.
    retry: false,
  });
}
