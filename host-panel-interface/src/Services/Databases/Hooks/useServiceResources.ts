import { useQuery } from "@tanstack/react-query";
import { databasesService, databasesServiceKeys } from "../databases.service";

/** Resources are read from the engine: only ask a ready instance. */
export default function useServiceResources(instance: string, enabled = true) {
  return useQuery({
    enabled,
    queryFn: () => databasesService.resources(instance),
    queryKey: [databasesServiceKeys.resources, instance],
    retry: false,
  });
}
