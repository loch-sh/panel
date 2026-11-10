import { useMutation, useQueryClient } from "@tanstack/react-query";
import { cronService, cronServiceKeys } from "../cron.service";

export default function useCronCreate() {
  const client = useQueryClient();
  return useMutation({
    mutationFn: cronService.create,
    onSettled: () => {
      client.invalidateQueries({ queryKey: [cronServiceKeys.jobs] });
    },
  });
}
