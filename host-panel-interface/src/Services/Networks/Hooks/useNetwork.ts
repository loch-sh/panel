import { useQuery } from "@tanstack/react-query";
import { networksService, networksServiceKeys } from "../networks.service";

/** A network with its members; polled faster while a change settles. */
export default function useNetwork(
  name: string,
  { enabled = true, fastPoll = false } = {},
) {
  return useQuery({
    enabled: enabled && name !== "",
    queryFn: () => networksService.get(name),
    queryKey: [networksServiceKeys.network, name],
    refetchInterval: fastPoll ? 2_000 : 15_000,
    retry: false,
  });
}
