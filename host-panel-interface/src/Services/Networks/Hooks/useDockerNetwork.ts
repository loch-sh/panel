import { useQuery } from "@tanstack/react-query";
import { networksService, networksServiceKeys } from "../networks.service";

/** A Docker network the panel did not create, with its containers. */
export default function useDockerNetwork(id: string, enabled = true) {
  return useQuery({
    enabled: enabled && id !== "",
    queryFn: () => networksService.getDocker(id),
    queryKey: [networksServiceKeys.dockerNetwork, id],
    refetchInterval: 30_000,
    retry: false,
  });
}
