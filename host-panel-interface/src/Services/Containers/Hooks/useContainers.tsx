import { useQuery } from "@tanstack/react-query";
import {
  containersService,
  containersServiceKeys,
} from "../containers.service";
import { secondsToMilliseconds } from "date-fns";

export default function useContainers() {
  return useQuery({
    queryFn: containersService.list,
    queryKey: [containersServiceKeys.containers],
    refetchInterval: secondsToMilliseconds(5),
  });
}
