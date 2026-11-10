import { useMutation, useQueryClient } from "@tanstack/react-query";
import { ApiKeysService, ApiKeysServiceKeys } from "../apikeys.service";

export default function useDeleteApiKey(options?: {
  onError?: (err: unknown) => void;
}) {
  const client = useQueryClient();
  return useMutation({
    mutationFn: ApiKeysService.delete,
    onSettled: () => {
      client.invalidateQueries({
        queryKey: [ApiKeysServiceKeys.apiKeys],
      });
    },
    onError: options?.onError,
  });
}
