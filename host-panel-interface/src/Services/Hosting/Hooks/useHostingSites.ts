import { useQuery } from "@tanstack/react-query";
import { hostingService, hostingServiceKeys } from "../hosting.service";

export default function useHostingSites(enabled = true) {
  return useQuery({
    queryFn: hostingService.listSites,
    queryKey: [hostingServiceKeys.sites],
    enabled,
  });
}
