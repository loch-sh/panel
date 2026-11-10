import { useQuery } from "@tanstack/react-query";
import { isAxiosError } from "axios";
import { functionsService, functionsServiceKeys } from "../functions.service";

/** One function, fully verified by the API (secret names only). */
export default function useFunction(name: string, enabled = true) {
  return useQuery({
    enabled: enabled && name !== "",
    queryFn: () => functionsService.get(name),
    queryKey: [functionsServiceKeys.function, name],
    refetchInterval: 10_000,
    retry: false,
  });
}

export function isFunctionNotFound(err: unknown): boolean {
  return isAxiosError(err) && err.response?.status === 404;
}
