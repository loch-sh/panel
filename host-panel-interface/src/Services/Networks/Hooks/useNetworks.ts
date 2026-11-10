import { useQuery } from "@tanstack/react-query";
import { networksService, networksServiceKeys } from "../networks.service";

/** `enabled`: false for non-admins, the endpoint would answer 403. */
export default function useNetworks(enabled = true) {
  return useQuery({
    enabled,
    queryFn: networksService.list,
    queryKey: [networksServiceKeys.networks],
    refetchInterval: 15_000,
    retry: false,
  });
}
