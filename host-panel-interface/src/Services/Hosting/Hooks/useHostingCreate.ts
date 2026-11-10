import { useMutation, useQueryClient } from "@tanstack/react-query";
import { hostingService, hostingServiceKeys } from "../hosting.service";

export default function useHostingCreate() {
  const client = useQueryClient();
  return useMutation({
    mutationFn: hostingService.create,
    onSuccess: (res) => {
      // A stack just deleted under the same name may still be cached as 404.
      client.removeQueries({ queryKey: [hostingServiceKeys.stack, res.name] });
    },
    onSettled: () => {
      client.invalidateQueries({ queryKey: [hostingServiceKeys.stacks] });
      client.invalidateQueries({ queryKey: [hostingServiceKeys.stack] });
      client.invalidateQueries({ queryKey: [hostingServiceKeys.sites] });
    },
  });
}
