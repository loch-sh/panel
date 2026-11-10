import { useQuery } from "@tanstack/react-query";
import { databasesService, databasesServiceKeys } from "../databases.service";

export default function useServiceInstances(enabled = true) {
  return useQuery({
    enabled,
    queryFn: databasesService.instances,
    queryKey: [databasesServiceKeys.instances],
    refetchInterval: 15_000,
  });
}
