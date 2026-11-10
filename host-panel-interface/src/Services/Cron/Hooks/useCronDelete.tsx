import { useMutation, useQueryClient } from "@tanstack/react-query";
import { cronService, cronServiceKeys } from "../cron.service";

export default function useCronDelete() {
  const client = useQueryClient();
  return useMutation({
    mutationFn: cronService.delete,
    onSettled: () => {
      client.invalidateQueries({ queryKey: [cronServiceKeys.jobs] });
    },
  });
}
