import { useQuery } from "@tanstack/react-query";
import { cronService, cronServiceKeys } from "../cron.service";
import { secondsToMilliseconds } from "date-fns";

export default function useCronJobs() {
  return useQuery({
    queryFn: cronService.list,
    queryKey: [cronServiceKeys.jobs],
    refetchInterval: secondsToMilliseconds(5),
  });
}
