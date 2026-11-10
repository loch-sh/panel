import { useQuery } from "@tanstack/react-query";
import { functionsService, functionsServiceKeys } from "../functions.service";

/** `enabled`: the module is enabled (admins only). */
export default function useFunctions(enabled = true) {
  return useQuery({
    enabled,
    queryFn: functionsService.list,
    queryKey: [functionsServiceKeys.functions],
    refetchInterval: 10_000,
    retry: false,
  });
}
