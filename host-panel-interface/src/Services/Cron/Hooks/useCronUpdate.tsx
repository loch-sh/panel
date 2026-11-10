import { useMutation, useQueryClient } from "@tanstack/react-query";
import { cronService, cronServiceKeys } from "../cron.service";

export default function useCronUpdate() {
  const client = useQueryClient();
  return useMutation({
    mutationFn: cronService.update,
    onSettled: () => {
      client.invalidateQueries({ queryKey: [cronServiceKeys.jobs] });
    },
  });
}
