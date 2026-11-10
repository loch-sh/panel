import { useQuery } from "@tanstack/react-query";
import {
  containersService,
  containersServiceKeys,
} from "../containers.service";
import { secondsToMilliseconds } from "date-fns";

export default function useContainer({ id }: { id?: string }) {
  return useQuery({
    queryFn: () => containersService.inspect(id!),
    queryKey: [containersServiceKeys.containers, id],
    refetchInterval: secondsToMilliseconds(5),
    enabled: !!id,
  });
}
