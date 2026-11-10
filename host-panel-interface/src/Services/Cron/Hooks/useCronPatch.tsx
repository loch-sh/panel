import { useMutation, useQueryClient } from "@tanstack/react-query";
import { cronService, cronServiceKeys } from "../cron.service";
import { CronJobPatchPayload } from "../Models/cron.model";

export default function useCronPatch() {
  const client = useQueryClient();
  return useMutation({
    mutationFn: ({ id, ...payload }: CronJobPatchPayload & { id: string }) =>
      cronService.patch(id, payload),
    onSettled: () => {
      client.invalidateQueries({ queryKey: [cronServiceKeys.jobs] });
    },
  });
}
