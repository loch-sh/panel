import { useMutation, useQueryClient } from "@tanstack/react-query";
import {
  containersService,
  containersServiceKeys,
} from "../containers.service";

export default function useContainerAction() {
  const client = useQueryClient();
  return useMutation({
    mutationFn: containersService.action,
    onSettled: () => {
      client.invalidateQueries({
        queryKey: [containersServiceKeys.containers],
      });
    },
  });
}
