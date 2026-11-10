import { useMutation, useQueryClient } from "@tanstack/react-query";
import { cronService, cronServiceKeys } from "../cron.service";

export default function useCronRun() {
  const client = useQueryClient();
  return useMutation({
    mutationFn: cronService.run,
    onSettled: () => {
      client.invalidateQueries({ queryKey: [cronServiceKeys.jobs] });
    },
  });
}
