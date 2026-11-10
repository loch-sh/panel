import { useQuery } from "@tanstack/react-query";
import { networksService, networksServiceKeys } from "../networks.service";

/** Creation rules of the private networks (admins only). */
export default function useNetworksConfig(enabled = true) {
  return useQuery({
    enabled,
    queryFn: networksService.config,
    queryKey: [networksServiceKeys.config],
    staleTime: 30_000,
    retry: false,
  });
}
