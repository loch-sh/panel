import { useMutation, useQueryClient } from "@tanstack/react-query";
import { ApiKeysService, ApiKeysServiceKeys } from "../apikeys.service";

export default function useCreateApiKey() {
  const client = useQueryClient();
  return useMutation({
    mutationFn: ApiKeysService.create,
    onSettled: () => {
      client.invalidateQueries({
        queryKey: [ApiKeysServiceKeys.apiKeys],
      });
    },
  });
}
