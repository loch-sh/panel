import { useQuery } from "@tanstack/react-query";
import { ApiKeysService, ApiKeysServiceKeys } from "../apikeys.service";

export default function useApiKeys() {
  return useQuery({
    queryKey: [ApiKeysServiceKeys.apiKeys],
    queryFn: ApiKeysService.list,
  });
}
