import { useMutation, useQueryClient } from "@tanstack/react-query";
import { hostingService, hostingServiceKeys } from "../hosting.service";

export default function useHostingUpdate() {
  const client = useQueryClient();
  return useMutation({
    mutationFn: hostingService.update,
    onSettled: () => {
      client.invalidateQueries({ queryKey: [hostingServiceKeys.stacks] });
      client.invalidateQueries({ queryKey: [hostingServiceKeys.stack] });
      client.invalidateQueries({ queryKey: [hostingServiceKeys.sites] });
    },
  });
}
