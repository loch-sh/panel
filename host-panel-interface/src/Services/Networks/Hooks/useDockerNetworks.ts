import { useQuery } from "@tanstack/react-query";
import { networksService, networksServiceKeys } from "../networks.service";

/** Docker networks the panel did not create (admins only, read-only). */
export default function useDockerNetworks(enabled = true) {
  return useQuery({
    enabled,
    queryFn: networksService.listDocker,
    queryKey: [networksServiceKeys.dockerNetworks],
    refetchInterval: 30_000,
    retry: false,
  });
}
